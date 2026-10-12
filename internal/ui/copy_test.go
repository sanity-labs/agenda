package ui

import (
	"strings"
	"testing"
)

// Copying always answers with a toast, so the key is seen to have worked
// (or not): the copied text when it did, the reason when it did not.
func TestCopyCmdAnswersWithAToast(t *testing.T) {
	if CopyCmd("", "URL") != nil {
		t.Error("nothing to copy should be a no-op")
	}
	msg, ok := CopyCmd("https://x/y", "URL")().(ToastMsg)
	if !ok {
		t.Fatalf("copy returned %T, want a toast", msg)
	}
	if msg.Success && (msg.Title != "Copied URL" || msg.Body != "https://x/y") {
		t.Errorf("success toast = %+v", msg)
	}
	if !msg.Success && !strings.Contains(msg.Title, "failed") {
		t.Errorf("failure toast = %+v", msg)
	}
}
