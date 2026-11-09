package auth

import (
	"net/mail"
	"regexp"
)

const (
	maxEmailLen      = 254
	minPasswordBytes = 8
	maxPasswordBytes = 72 // bcrypt ignores everything after 72 bytes
	maxFullNameRunes = 100

	phoneMessage = "must be 8 to 15 digits, with an optional leading +"
)

var phonePattern = regexp.MustCompile(`^\+?[0-9]{8,15}$`)

// validEmail expects an already normalized address.
func validEmail(email string) bool {
	if len(email) > maxEmailLen {
		return false
	}
	addr, err := mail.ParseAddress(email)
	return err == nil && addr.Address == email
}

func validPhone(phone string) bool {
	return phonePattern.MatchString(phone)
}

func between(n, minimum, maximum int) bool {
	return n >= minimum && n <= maximum
}
