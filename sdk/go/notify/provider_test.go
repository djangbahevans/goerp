package notify

import "testing"

func TestProviderDefinitions_NameTheirCategoryAndJobType(t *testing.T) {
	if SMSSend.Category() != "sms_provider" || SMSSend.Name() != "sms_send" {
		t.Errorf("SMSSend = %q on %q, want sms_send on sms_provider", SMSSend.Name(), SMSSend.Category())
	}
	if PushSend.Category() != "push_provider" || PushSend.Name() != "push_send" {
		t.Errorf("PushSend = %q on %q, want push_send on push_provider", PushSend.Name(), PushSend.Category())
	}
}
