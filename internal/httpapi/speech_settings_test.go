package httpapi

import (
	"testing"

	"github.com/owui-personal-slim/owui-personal-slim/internal/speech"
)

func TestEffectiveSpeechVoiceFallsBackAfterProviderSwitch(t *testing.T) {
	voices := []speech.Voice{{ID: "volc-voice", Label: "Volc Voice"}}
	if got := effectiveSpeechVoice("aliyun-voice", "volc-voice", voices); got != "volc-voice" {
		t.Fatalf("effective voice = %q, want provider default", got)
	}
	if got := effectiveSpeechVoice("volc-voice", "other", voices); got != "volc-voice" {
		t.Fatalf("effective voice = %q, want user preference", got)
	}
}
