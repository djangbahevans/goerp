package mailer

import (
	"strings"
	"testing"
)

func TestSendOperatorMFAReset(t *testing.T) {
	srv := startFakeSMTP(t)
	m := newTestMailer(t, srv, "", "")
	if err := m.SendOperatorMFAReset(t.Context(), "person@example.test"); err != nil {
		t.Fatal(err)
	}

	msg := waitForMessage(t, srv)
	if len(msg.to) != 1 || msg.to[0] != "<person@example.test>" {
		t.Fatalf("recipients = %v", msg.to)
	}

	for _, text := range []string{"Your two-factor authentication has been reset", "across all organisations", "signed you out everywhere", "contact platform support"} {
		if !strings.Contains(msg.data, text) {
			t.Errorf("email is missing %q: %s", text, msg.data)
		}
	}
}
