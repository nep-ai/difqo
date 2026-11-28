package controlplane

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nep-ai/difqo/internal/store"
)

const (
	maxAttachments      = 4
	maxAttachmentBytes  = 2 << 20
	maxTotalAttachments = 4 << 20
	maxTextAttachment   = 64 << 10
	maxMessageBody      = 6 << 20 // Includes base64 and the JSON envelope.
)

func validateAttachments(attachments []store.Attachment) error {
	if len(attachments) > maxAttachments {
		return errors.New("attach up to four files")
	}
	total := 0
	for i := range attachments {
		file := &attachments[i]
		if !utf8.ValidString(file.Name) || len(file.Name) > 255 || strings.TrimSpace(file.Name) == "" ||
			strings.ContainsAny(file.Name, "/\\") || strings.ContainsFunc(file.Name, unicode.IsControl) {
			return errors.New("an attachment has an invalid file name")
		}
		if len(file.Data) == 0 || len(file.Data) > maxAttachmentBytes {
			return errors.New("each attachment must be non-empty and at most 2 MB")
		}
		total += len(file.Data)
		if total > maxTotalAttachments {
			return errors.New("attachments must total at most 4 MB")
		}
		switch kind := http.DetectContentType(file.Data); kind {
		case "image/png", "image/jpeg":
			config, _, err := image.DecodeConfig(bytes.NewReader(file.Data))
			if err != nil || config.Width <= 0 || config.Height <= 0 ||
				config.Width > 4096 || config.Height > 4096 {
				return errors.New("attach an intact PNG or JPEG no larger than 4096 pixels on each side")
			}
			file.MIME = kind
		default:
			if len(file.Data) > maxTextAttachment || !utf8.Valid(file.Data) || bytes.ContainsRune(file.Data, 0) {
				return errors.New("attach PNG or JPEG images, or UTF-8 text files up to 64 KB")
			}
			file.MIME = "text/plain"
		}
	}
	return nil
}
