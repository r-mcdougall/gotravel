package payment

import "strings"

const hexDigits = "0123456789abcdefABCDEF"

func (r *PayRequest) normalize() {
	r.BookingID = strings.TrimSpace(r.BookingID)
	r.Outcome = strings.TrimSpace(r.Outcome)
	if r.Outcome == "" {
		r.Outcome = outcomeSucceeded
	}
}

func (r *PayRequest) Validate() error {
	var v ValidationError

	v.check(validUUID(r.BookingID), "booking_id", "must be a UUID")
	v.check(r.Outcome == outcomeSucceeded || r.Outcome == outcomeFailed, "outcome", "must be succeeded or failed")

	return v.err()
}

func validUUID(s string) bool {
	if len(s) != 36 {
		return false
	}

	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune(hexDigits, c) {
				return false
			}
		}
	}
	return true
}
