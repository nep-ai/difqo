package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/nep-ai/difqo/internal/store"
)

func runChat(args []string) error {
	flags := flag.NewFlagSet("chat", flag.ContinueOnError)
	port := flags.Int("port", 8765, "loopback API port")
	var files []store.Attachment
	var size int
	flags.Func("file", "attach a local PNG, JPEG, or UTF-8 file; repeat for additional files", func(path string) error {
		if len(files) == 4 {
			return errors.New("attach up to four files")
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("attachment must be a local regular file")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		raw, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
		if err != nil {
			return err
		}
		size += len(raw)
		if len(raw) == 0 || len(raw) > 2<<20 || size > 4<<20 {
			return errors.New("attachments must be non-empty and under 2 MB each and 4 MB total")
		}
		files = append(files, store.Attachment{Name: filepath.Base(path), Data: raw})
		return nil
	})
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	message := strings.TrimSpace(strings.Join(flags.Args(), " "))
	if *port < 1 || *port > 65535 || (message == "" && len(files) == 0) {
		return errors.New("usage: difqo chat [-file PATH] [MESSAGE]")
	}
	stopCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(stopCtx, 3*time.Minute)
	defer cancel()

	client := &http.Client{Timeout: 8 * time.Second}
	address := fmt.Sprintf("http://127.0.0.1:%d/chief/conversation", *port)
	request := func(method string, body []byte, result any) error {
		req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(body))
		if err != nil {
			return err
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode == http.StatusNotFound {
			return errors.New("Chief of Staff conversation is available in the installed Difqo Mac pilot")
		}
		if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusAccepted {
			detail, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
			return fmt.Errorf("Chief of Staff returned %d: %s", res.StatusCode, strings.TrimSpace(string(detail)))
		}
		return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(result)
	}
	var id [18]byte
	rand.Read(id[:])
	payload, err := json.Marshal(struct {
		RequestID   string             `json:"request_id"`
		Text        string             `json:"text"`
		Attachments []store.Attachment `json:"attachments,omitempty"`
	}{hex.EncodeToString(id[:]), message, files})
	if err != nil {
		return err
	}
	var turn store.ChatTurn
	if err := request(http.MethodPost, payload, &turn); err != nil {
		return err
	}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for turn.State == "running" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		var turns []store.ChatTurn
		if err := request(http.MethodGet, nil, &turns); err != nil {
			return err
		}
		for _, latest := range turns {
			if latest.ID == turn.ID {
				turn = latest
				break
			}
		}
	}
	if turn.State != "completed" {
		return errors.New(turn.Problem)
	}
	fmt.Println(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, turn.Reply))
	return nil
}
