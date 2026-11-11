package catalog

import (
	"regexp"
	"time"
)

const (
	defaultPage     int32 = 1
	defaultPageSize int32 = 20
	maxPage         int32 = 1_000_000 // keeps (page-1)*pageSize inside int32
	maxPageSize     int32 = 100

	maxPassengers int32 = 9
)

const (
	sortDepartureAsc  = "departure_asc"
	sortDepartureDesc = "departure_desc"
	sortPriceAsc      = "price_asc"
	sortPriceDesc     = "price_desc"
)

var (
	flightNumberPattern = regexp.MustCompile(`^[A-Z0-9]{2}[0-9]{1,4}$`)
	airlineCodePattern  = regexp.MustCompile(`^[A-Z0-9]{2}$`)
	airportCodePattern  = regexp.MustCompile(`^[A-Z]{3}$`)
	uuidPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

const (
	flightNumberMessage = "must be an airline code followed by 1 to 4 digits, in upper case"
	airlineCodeMessage  = "must be a 2-character airline code in upper case"
	airportCodeMessage  = "must be a 3-letter airport code in upper case"
	timeOfDayMessage    = "must be a time between 00:00 and 23:59"
)

func validUUID(id string) bool {
	return uuidPattern.MatchString(id)
}

func validSort(sort string) bool {
	switch sort {
	case sortDepartureAsc, sortDepartureDesc, sortPriceAsc, sortPriceDesc:
		return true
	default:
		return false
	}
}

// validTimeOfDay reports whether d is an offset from local midnight that falls inside the same day.
func validTimeOfDay(d time.Duration) bool {
	return d >= 0 && d < 24*time.Hour
}

func checkPaging(v *ValidationError, page, pageSize int32) {
	v.check(page >= 1 && page <= maxPage, "page", "must be between 1 and 1000000")
	v.check(pageSize >= 1 && pageSize <= maxPageSize, "page_size", "must be between 1 and 100")
}
