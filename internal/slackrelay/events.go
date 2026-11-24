package slackrelay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/slackintake"
	"github.com/nep-ai/difqo/internal/store"
)

const (
	maxWebhookBytes  = 64 << 10
	signatureAge     = 5 * time.Minute
	webhookDBTimeout = 2 * time.Second
)

func (r *relay) signedEvents(signingSecret string, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, maxWebhookBytes))
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
			} else {
				w.WriteHeader(http.StatusBadRequest)
			}
			return
		}
		if !verifySignature(req.Header, body, signingSecret, now()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var callback slackintake.Callback
		if !utf8.Valid(body) || json.Unmarshal(body, &callback) != nil || callback.Type == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if callback.Type == "url_verification" {
			if callback.Challenge == "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, callback.Challenge)
			return
		}

		binding, goal := callback.Goal()
		if goal != "" {
			ctx, cancel := context.WithTimeout(req.Context(), webhookDBTimeout)
			defer cancel()
			if err := r.pairings.enqueue(ctx, binding, callback.EventID, goal); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}
}

func verifySignature(headers http.Header, body []byte, secret string, now time.Time) bool {
	timestamps, signatures := headers.Values("X-Slack-Request-Timestamp"), headers.Values("X-Slack-Signature")
	if len(timestamps) != 1 || len(signatures) != 1 {
		return false
	}
	stamp, err := strconv.ParseInt(timestamps[0], 10, 64)
	if err != nil || stamp < now.Add(-signatureAge).Unix() || stamp > now.Add(signatureAge).Unix() {
		return false
	}
	digest, ok := strings.CutPrefix(signatures[0], "v0=")
	if !ok || len(digest) != sha256.Size*2 {
		return false
	}
	signature, err := hex.DecodeString(digest)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	io.WriteString(mac, "v0:"+timestamps[0]+":")
	mac.Write(body)
	return hmac.Equal(signature, mac.Sum(nil))
}

func (r *relay) claim(w http.ResponseWriter, req *http.Request) {
	id, proof, ok := pairingAuth(req)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var expected store.SlackBinding
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 2048))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&expected) != nil || decoder.Decode(new(any)) != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if err := r.pairings.claim(req.Context(), id, proof, expected); err != nil {
		pairingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
