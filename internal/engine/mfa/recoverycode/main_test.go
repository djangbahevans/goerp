package recoverycode

import (
	"os"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestMain(m *testing.M) {
	SetHashCost(bcrypt.MinCost)
	os.Exit(m.Run())
}
