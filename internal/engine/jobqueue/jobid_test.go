package jobqueue

import "testing"

func TestEncodeDecodeJobID_RoundTrips(t *testing.T) {
	id, err := DecodeJobID(EncodeJobID(42))
	if err != nil {
		t.Fatalf("DecodeJobID: %v", err)
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
}

func TestDecodeJobID_RejectsMissingPrefix(t *testing.T) {
	if _, err := DecodeJobID("42"); err == nil {
		t.Fatal("expected an error for a job id missing the job_ prefix")
	}
}

func TestDecodeJobID_RejectsNonNumeric(t *testing.T) {
	if _, err := DecodeJobID("job_abc"); err == nil {
		t.Fatal("expected an error for a non-numeric job id")
	}
}
