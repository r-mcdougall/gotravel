package booking

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const hexDigits = "0123456789abcdefABCDEF"

func (r *HoldRequest) Validate() error {
	var v ValidationError

	v.check(validUUID(r.FlightID), "flight_id", "must be a UUID")
	v.check(len(r.Passengers) >= 1 && len(r.Passengers) <= maxPassengers,
		"passengers", fmt.Sprintf("must list between 1 and %d passengers", maxPassengers))

	today := time.Now().UTC().Format(time.DateOnly)
	for i, p := range r.Passengers {
		nameLen := utf8.RuneCountInString(p.FullName)
		v.check(nameLen >= 1 && nameLen <= maxFullNameLen,
			fmt.Sprintf("passengers[%d].full_name", i), fmt.Sprintf("must be between 1 and %d characters", maxFullNameLen))

		_, err := time.Parse(time.DateOnly, p.BirthDate)
		v.check(err == nil && p.BirthDate <= today,
			fmt.Sprintf("passengers[%d].birth_date", i), "must be a past date in YYYY-MM-DD format")
	}

	return v.err()
}

func (r *HotelHoldRequest) Validate() error {
	var v ValidationError

	v.check(validUUID(r.RoomTypeID), "room_type_id", "must be a UUID")

	checkIn, checkInErr := time.Parse(time.DateOnly, r.CheckIn)
	checkOut, checkOutErr := time.Parse(time.DateOnly, r.CheckOut)
	v.check(checkInErr == nil, "check_in", "must be a date in YYYY-MM-DD format")
	v.check(checkOutErr == nil, "check_out", "must be a date in YYYY-MM-DD format")
	if checkInErr == nil && checkOutErr == nil {
		v.check(checkOut.After(checkIn), "check_out", "must be after check_in")
		v.check(nights(checkIn, checkOut) <= maxNights, "check_out", fmt.Sprintf("must be at most %d nights after check_in", maxNights))
	}

	v.check(r.Guests >= 1 && r.Guests <= maxGuests, "guests", fmt.Sprintf("must be between 1 and %d", maxGuests))
	nameLen := utf8.RuneCountInString(r.GuestName)
	v.check(nameLen >= 1 && nameLen <= maxFullNameLen, "guest_name", fmt.Sprintf("must be between 1 and %d characters", maxFullNameLen))

	return v.err()
}

// stay returns the dates of a request that passed Validate, as midnight UTC.
func (r *HotelHoldRequest) stay() (checkIn, checkOut time.Time) {
	checkIn, _ = time.Parse(time.DateOnly, r.CheckIn)
	checkOut, _ = time.Parse(time.DateOnly, r.CheckOut)
	return checkIn, checkOut
}

// nights is the length of a stay whose dates are both midnight UTC.
func nights(checkIn, checkOut time.Time) int64 {
	return int64(checkOut.Sub(checkIn) / (24 * time.Hour))
}

// checkCheckIn rejects a stay that starts before today. Today is the UTC date, so east of UTC a check-in
// on the local yesterday still passes for a few hours after local midnight.
func checkCheckIn(checkIn, now time.Time) error {
	year, month, day := now.UTC().Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)

	var v ValidationError
	v.check(!checkIn.Before(today), "check_in", "must not be in the past")
	return v.err()
}

func (r ListRequest) Validate() error {
	var v ValidationError

	v.check(r.Page >= 1 && r.Page <= maxPage, "page", fmt.Sprintf("must be between 1 and %d", maxPage))
	v.check(r.PageSize >= 1 && r.PageSize <= maxPageSize, "page_size", fmt.Sprintf("must be between 1 and %d", maxPageSize))

	return v.err()
}

func (r *AnalyticsRequest) normalize(today time.Time) {
	r.From = strings.TrimSpace(r.From)
	r.To = strings.TrimSpace(r.To)
	if r.To == "" {
		r.To = today.UTC().Format(time.DateOnly)
	}
	if to, err := time.Parse(time.DateOnly, r.To); r.From == "" && err == nil {
		r.From = to.AddDate(0, 0, 1-defaultAnalyticsDays).Format(time.DateOnly)
	}
}

func (r *AnalyticsRequest) Validate() error {
	var v ValidationError

	from, fromErr := time.Parse(time.DateOnly, r.From)
	to, toErr := time.Parse(time.DateOnly, r.To)
	// From is empty only when normalize could not derive it from an invalid to.
	v.check(r.From == "" || fromErr == nil, "from", "must be a date in YYYY-MM-DD format")
	v.check(toErr == nil, "to", "must be a date in YYYY-MM-DD format")
	if fromErr == nil && toErr == nil {
		v.check(!to.Before(from), "to", "must not be before from")
		v.check(!to.After(from.AddDate(0, 0, maxAnalyticsDays-1)), "to", fmt.Sprintf("must be at most %d days after from", maxAnalyticsDays-1))
	}

	return v.err()
}

func (r *HoldRequest) normalize() {
	r.FlightID = strings.TrimSpace(r.FlightID)
	for i := range r.Passengers {
		r.Passengers[i].FullName = strings.TrimSpace(r.Passengers[i].FullName)
		r.Passengers[i].BirthDate = strings.TrimSpace(r.Passengers[i].BirthDate)
	}
}

func (r *HotelHoldRequest) normalize() {
	r.RoomTypeID = strings.TrimSpace(r.RoomTypeID)
	r.CheckIn = strings.TrimSpace(r.CheckIn)
	r.CheckOut = strings.TrimSpace(r.CheckOut)
	r.GuestName = strings.TrimSpace(r.GuestName)
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
