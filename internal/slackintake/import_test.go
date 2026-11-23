package slackintake

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

func TestReadEventCancelsWhileWaitingForInput(t *testing.T) {
	for name, input := range map[string]string{
		"incomplete document":           "{",
		"complete document without EOF": "{}",
	} {
		t.Run(name, func(t *testing.T) {
			reader, writer := io.Pipe()
			t.Cleanup(func() {
				reader.Close()
				writer.Close()
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := readEvent(ctx, reader)
				result <- err
			}()
			written := make(chan error, 1)
			go func() {
				_, err := io.WriteString(writer, input)
				written <- err
			}()
			select {
			case err := <-written:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("event reader did not consume input")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("readEvent returned %v, want context cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("event reader did not return after cancellation")
			}
		})
	}
}
