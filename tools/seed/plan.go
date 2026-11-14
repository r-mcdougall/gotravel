package main

import (
	"errors"
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"time"
)

var airportZones = map[string]string{
	"CGK": "Asia/Jakarta",
	"SUB": "Asia/Jakarta",
	"KNO": "Asia/Jakarta",
	"YIA": "Asia/Jakarta",
	"DPS": "Asia/Makassar",
	"UPG": "Asia/Makassar",
	"SIN": "Asia/Singapore",
	"KUL": "Asia/Kuala_Lumpur",
	"HND": "Asia/Tokyo",
}

var airlineProfiles = map[string]airlineProfile{
	"GA": {Capacity: 180, PriceFactor: 1.20},
	"QG": {Capacity: 180, PriceFactor: 0.90},
	"JT": {Capacity: 215, PriceFactor: 0.80},
	"ID": {Capacity: 160, PriceFactor: 1.00},
	"IU": {Capacity: 180, PriceFactor: 0.80},
	"QZ": {Capacity: 180, PriceFactor: 0.75},
	"SQ": {Capacity: 250, PriceFactor: 1.30},
	"NH": {Capacity: 240, PriceFactor: 1.30},
}

type airlineProfile struct {
	Capacity    int32
	PriceFactor float64
}

// route is one direction between two airports with a fixed daily timetable.
type route struct {
	Origin           string
	Destination      string
	Slots            int      // flights a day
	Airlines         []string // take turns over the slots
	FlightNumberBase int      // numbers run upward from here, per airline
	DurationMinutes  int
	BasePrice        int64 // rupiah, before the airline and time-of-day factors
}

var domesticAirlines = []string{"GA", "QG", "JT", "ID", "IU", "QZ"}

// routes is the timetable the seed creates: one hot route for load testing and a handful of quieter ones.
var routes = []route{
	{"CGK", "DPS", 300, domesticAirlines, 100, 115, 1_200_000},
	{"DPS", "CGK", 60, domesticAirlines, 200, 115, 1_200_000},
	{"CGK", "SUB", 40, domesticAirlines, 300, 95, 900_000},
	{"SUB", "CGK", 40, domesticAirlines, 400, 95, 900_000},
	{"CGK", "KNO", 30, domesticAirlines, 500, 155, 1_400_000},
	{"CGK", "YIA", 30, domesticAirlines, 600, 70, 800_000},
	{"CGK", "UPG", 25, domesticAirlines, 700, 190, 1_700_000},
	{"SUB", "DPS", 20, domesticAirlines, 800, 75, 700_000},
	{"CGK", "SIN", 20, []string{"SQ", "GA"}, 900, 110, 1_800_000},
	{"SIN", "CGK", 20, []string{"SQ", "GA"}, 1000, 110, 1_800_000},
	{"CGK", "KUL", 15, []string{"GA", "QZ"}, 1100, 120, 1_500_000},
	{"CGK", "HND", 6, []string{"NH", "GA"}, 1200, 430, 6_500_000},
}

// flightSpec is the body of POST /api/v1/admin/flights.
type flightSpec struct {
	FlightNumber    string    `json:"flight_number"`
	AirlineCode     string    `json:"airline_code"`
	OriginCode      string    `json:"origin_code"`
	DestinationCode string    `json:"destination_code"`
	DepartureAt     time.Time `json:"departure_at"`
	ArrivalAt       time.Time `json:"arrival_at"`
	Price           int64     `json:"price"`
	Capacity        int32     `json:"capacity"`
}

// plan lists every flight of the timetable for the given number of local days, starting at the calendar date of from.
func plan(from time.Time, days int) ([]flightSpec, error) {
	if days < 1 {
		return nil, errors.New("days must be at least 1")
	}

	zones := make(map[string]*time.Location, len(airportZones))
	for code, name := range airportZones {
		location, err := time.LoadLocation(name)
		if err != nil {
			return nil, fmt.Errorf("load time zone of %s: %w", code, err)
		}
		zones[code] = location
	}

	timetables := make([][]slot, len(routes))
	total := 0
	for i, r := range routes {
		timetable, err := newTimetable(r)
		if err != nil {
			return nil, err
		}
		timetables[i] = timetable
		total += len(timetable)
	}

	year, month, day := from.Date()
	flights := make([]flightSpec, 0, total*days)
	for d := range days {
		for i, r := range routes {
			origin, destination := zones[r.Origin], zones[r.Destination]
			midnight := time.Date(year, month, day+d, 0, 0, 0, 0, origin)

			for _, s := range timetables[i] {
				departure := midnight.Add(s.Departure)
				flights = append(flights, flightSpec{
					FlightNumber:    s.FlightNumber,
					AirlineCode:     s.AirlineCode,
					OriginCode:      r.Origin,
					DestinationCode: r.Destination,
					DepartureAt:     departure,
					ArrivalAt:       departure.Add(s.Duration).In(destination),
					Price:           price(r, s, midnight),
					Capacity:        s.Capacity,
				})
			}
		}
	}

	return flights, nil
}

// slot is one flight of a route's daily timetable. It repeats every day.
type slot struct {
	Index        int
	FlightNumber string
	AirlineCode  string
	Departure    time.Duration // from local midnight
	Duration     time.Duration
	Capacity     int32
}

func newTimetable(r route) ([]slot, error) {
	if r.Slots < 1 || len(r.Airlines) == 0 {
		return nil, fmt.Errorf("route %s-%s has no flights", r.Origin, r.Destination)
	}

	timetable := make([]slot, r.Slots)
	for i := range timetable {
		code := r.Airlines[i%len(r.Airlines)]
		profile, ok := airlineProfiles[code]
		if !ok {
			return nil, fmt.Errorf("route %s-%s: unknown airline %s", r.Origin, r.Destination, code)
		}
		number := r.FlightNumberBase + i/len(r.Airlines)

		timetable[i] = slot{
			Index:        i,
			FlightNumber: code + strconv.Itoa(number),
			AirlineCode:  code,
			Departure:    time.Duration(i*24*60/r.Slots) * time.Minute, // spread evenly over the day
			Duration:     time.Duration(r.DurationMinutes) * time.Minute,
			Capacity:     profile.Capacity,
		}
	}

	return timetable, nil
}

const priceStep = 5_000

// price is the base price of the route, scaled by the airline and the time of day, with a little noise that depends only on the route, slot and date.
func price(r route, s slot, localMidnight time.Time) int64 {
	value := float64(r.BasePrice) * airlineProfiles[s.AirlineCode].PriceFactor * timeOfDayFactor(s.Departure)

	hash := fnv.New64a()
	_, _ = fmt.Fprintf(hash, "%s-%s-%d-%s", r.Origin, r.Destination, s.Index, localMidnight.Format(time.DateOnly))
	seed := hash.Sum64()
	// A seeded generator on purpose: the same flight gets the same price on every run.
	noise := 0.9 + 0.2*rand.New(rand.NewPCG(seed, seed>>1)).Float64() //nolint:gosec // not security related, determinism is the goal

	return int64(value*noise/priceStep+0.5) * priceStep
}

func timeOfDayFactor(departure time.Duration) float64 {
	hour := int(departure / time.Hour)
	switch {
	case hour >= 6 && hour < 9, hour >= 16 && hour < 20:
		return 1.25 // commuting peaks
	case hour >= 22 || hour < 5:
		return 0.85 // late and red-eye flights
	default:
		return 1.0
	}
}
