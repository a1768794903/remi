package voicechat

import "testing"

func TestVoiceChatFilesUsesFirstAudioPart(t *testing.T) {
	if got := voiceChatFileNames([]string{"first.wav", "second.wav"}); len(got) != 1 || got[0] != "first.wav" {
		t.Fatalf("unexpected selected files: %#v", got)
	}
}

func TestVoiceChatSSEEventEncodesJSONPayload(t *testing.T) {
	event, err := doneEvent(map[string]string{"id": "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if event[:6] != "done: " || event[len(event)-2:] != "\n\n" {
		t.Fatalf("unexpected event %q", event)
	}
}
