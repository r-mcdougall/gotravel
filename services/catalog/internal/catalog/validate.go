package catalog

import "regexp"

const (
	defaultPage     int32 = 1
	defaultPageSize int32 = 20
	maxPage         int32 = 1_000_000 // keeps (page-1)*pageSize inside int32
	maxPageSize     int32 = 100
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
)

func validUUID(id string) bool {
	return uuidPattern.MatchString(id)
}
