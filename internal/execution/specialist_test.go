package execution

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSnapshotResultFitsRequestLimit(t *testing.T) {
	output := strings.Repeat("🧪", 4096*2) + strings.Repeat("<", 4096) + strings.Repeat("\t", 24<<10)
	result := snapshotResult(output, true)
	payload, err := json.Marshal(result)
	if err != nil || len(payload) > 64<<10 {
		t.Fatalf("snapshot result is %d bytes, marshal error %v", len(payload), err)
	}
	if !utf8.ValidString(result.Output) || !strings.HasSuffix(result.Output, "\nSnapshot report truncated.\n") {
		t.Fatal("expected valid UTF-8 with a truncation notice")
	}
}
