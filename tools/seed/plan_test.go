package main

import (
	"encoding/json"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var planStart = time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

const flightsPerDay = 606 // the sum of the slots in the route table

var (
	knownAirlines = []string{"GA", "QG", "JT", "ID", "IU", "QZ", "SQ", "NH"}
	knownAirports = []string{"CGK", "SUB", "KNO", "YIA", "DPS", "UPG", "BPN", "DJJ", "SIN", "KUL", "HND"}
	flightNumber  = regexp.MustCompile(`^[A-Z0-9]{2}[0-9]{1,4}$`)
)

func mustPlan(t *testing.T, days int) []flightSpec {
	t.Helper()
	flights, err := plan(planStart, days)
	require.NoError(t, err)
	return flights
}

func localDate(t *testing.T, instant time.Time, airport string) string {
	t.Helper()
	location, err := time.LoadLocation(airportZones[airport])
	require.NoError(t, err)
	return instant.In(location).Format(time.DateOnly)
}

func TestPlan_createsTheWholeTimetableEveryDay(t *testing.T) {
	flights := mustPlan(t, 14)

	require.Len(t, flights, 14*flightsPerDay)
}

func TestPlan_putsThreeHundredFlightsADayOnTheHotRoute(t *testing.T) {
	perDay := map[string]int{}
	for _, f := range mustPlan(t, 3) {
		if f.OriginCode == "CGK" && f.DestinationCode == "DPS" {
			perDay[localDate(t, f.DepartureAt, "CGK")]++
		}
	}

	require.Equal(t, map[string]int{"2026-11-20": 300, "2026-11-21": 300, "2026-11-22": 300}, perDay)
}

func TestPlan_neverRepeatsAFlightsKey(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range mustPlan(t, 14) {
		key := f.AirlineCode + "|" + f.FlightNumber + "|" + f.DepartureAt.UTC().Format(time.RFC3339)
		require.False(t, seen[key], "duplicate flight %s", key)
		seen[key] = true
	}
}

func TestPlan_buildsFlightsTheCatalogAccepts(t *testing.T) {
	for _, f := range mustPlan(t, 2) {
		require.Regexp(t, flightNumber, f.FlightNumber)
		require.Equal(t, f.AirlineCode, f.FlightNumber[:2], "the number starts with its airline")
		require.Contains(t, knownAirlines, f.AirlineCode)
		require.Contains(t, knownAirports, f.OriginCode)
		require.Contains(t, knownAirports, f.DestinationCode)
		require.NotEqual(t, f.OriginCode, f.DestinationCode)
		require.Positive(t, f.Capacity)
	}
}

func TestPlan_isTheSameEveryTime(t *testing.T) {
	first, err := json.Marshal(mustPlan(t, 5))
	require.NoError(t, err)
	second, err := json.Marshal(mustPlan(t, 5))
	require.NoError(t, err)

	require.JSONEq(t, string(first), string(second))
}

func TestPlan_repeatsTheTimetableEachDayAndOnlyThePriceMoves(t *testing.T) {
	flights := mustPlan(t, 2)
	today, tomorrow := flights[:flightsPerDay], flights[flightsPerDay:]

	pricesDiffer := false
	for i := range today {
		require.Equal(t, today[i].FlightNumber, tomorrow[i].FlightNumber)
		require.Equal(t, today[i].AirlineCode, tomorrow[i].AirlineCode)
		require.Equal(t, today[i].OriginCode, tomorrow[i].OriginCode)
		require.Equal(t, today[i].Capacity, tomorrow[i].Capacity)
		require.Equal(t, today[i].DepartureAt.Add(24*time.Hour).Unix(), tomorrow[i].DepartureAt.Unix(), "same local time a day later")
		pricesDiffer = pricesDiffer || today[i].Price != tomorrow[i].Price
	}
	require.True(t, pricesDiffer, "prices change from day to day")
}

func TestPlan_departsInsideTheLocalDayAtTheOriginAirport(t *testing.T) {
	var firstOfDay, lastOfDay time.Time
	for _, f := range mustPlan(t, 1) {
		require.Equal(t, "2026-11-20", localDate(t, f.DepartureAt, f.OriginCode), f.FlightNumber)
		if f.OriginCode == "CGK" && f.DestinationCode == "DPS" {
			if firstOfDay.IsZero() || f.DepartureAt.Before(firstOfDay) {
				firstOfDay = f.DepartureAt
			}
			if f.DepartureAt.After(lastOfDay) {
				lastOfDay = f.DepartureAt
			}
		}
	}

	require.Equal(t, "2026-11-20T00:00:00+07:00", firstOfDay.Format(time.RFC3339), "the day starts at local midnight")
	require.Equal(t, "2026-11-20T23:55:00+07:00", lastOfDay.Format(time.RFC3339), "and the last flight leaves before the next one")
}

func TestPlan_showsEachTimeInItsOwnAirportOffset(t *testing.T) {
	var found bool
	for _, f := range mustPlan(t, 1) {
		if f.FlightNumber == "GA100" {
			found = true
			require.Equal(t, "2026-11-20T00:00:00+07:00", f.DepartureAt.Format(time.RFC3339))
			require.Equal(t, "2026-11-20T02:55:00+08:00", f.ArrivalAt.Format(time.RFC3339), "115 minutes later, an hour ahead in Bali time")
		}
	}
	require.True(t, found)
}

func TestPlan_landsAfterTakeOffByTheRouteDuration(t *testing.T) {
	durations := map[string]time.Duration{}
	for _, r := range routes {
		durations[r.Origin+r.Destination] = time.Duration(r.DurationMinutes) * time.Minute
	}

	for _, f := range mustPlan(t, 1) {
		require.Equal(t, durations[f.OriginCode+f.DestinationCode], f.ArrivalAt.Sub(f.DepartureAt), f.FlightNumber)
	}
}

func TestPlan_roundsPricesAndKeepsThemRealistic(t *testing.T) {
	for _, f := range mustPlan(t, 14) {
		require.Zero(t, f.Price%5_000, "%s costs %d", f.FlightNumber, f.Price)
		require.GreaterOrEqual(t, f.Price, int64(300_000), f.FlightNumber)
		require.LessOrEqual(t, f.Price, int64(12_000_000), f.FlightNumber)
	}
}

func TestPlan_chargesMoreForFullServiceAirlinesAndPeakHours(t *testing.T) {
	average := func(match func(flightSpec) bool) float64 {
		var sum, count int64
		for _, f := range mustPlan(t, 14) {
			if f.OriginCode == "CGK" && f.DestinationCode == "DPS" && match(f) {
				sum += f.Price
				count++
			}
		}
		require.NotZero(t, count)
		return float64(sum) / float64(count)
	}
	hourOf := func(f flightSpec) int { return f.DepartureAt.Hour() } // already in Jakarta time

	garuda := average(func(f flightSpec) bool { return f.AirlineCode == "GA" })
	airAsia := average(func(f flightSpec) bool { return f.AirlineCode == "QZ" })
	peak := average(func(f flightSpec) bool { return slices.Contains([]int{7, 8, 17, 18}, hourOf(f)) })
	night := average(func(f flightSpec) bool { return slices.Contains([]int{23, 0, 1, 2}, hourOf(f)) })

	require.Greater(t, garuda, airAsia*1.4)
	require.Greater(t, peak, night*1.2)
}

func TestPlan_rejectsFewerThanOneDay(t *testing.T) {
	for _, days := range []int{0, -1} {
		_, err := plan(planStart, days)

		require.ErrorContains(t, err, "days must be at least 1")
	}
}

func TestRoutes_haveEnoughOfEverythingToBuildATimetable(t *testing.T) {
	total := 0
	for _, r := range routes {
		_, err := newTimetable(r)
		require.NoError(t, err, "%s-%s", r.Origin, r.Destination)
		require.Contains(t, airportZones, r.Origin)
		require.Contains(t, airportZones, r.Destination)
		total += r.Slots
	}

	require.Equal(t, flightsPerDay, total)
}

func TestNewTimetable_rejectsARouteWithoutFlightsOrWithAnUnknownAirline(t *testing.T) {
	_, err := newTimetable(route{Origin: "CGK", Destination: "DPS", Slots: 0, Airlines: []string{"GA"}})
	require.ErrorContains(t, err, "no flights")

	_, err = newTimetable(route{Origin: "CGK", Destination: "DPS", Slots: 3, Airlines: []string{"ZZ"}})
	require.ErrorContains(t, err, "unknown airline ZZ")
}

func TestPlan_numbersFlightsPerAirlineStartingAtTheRouteBase(t *testing.T) {
	var firstFour, lastOfDay []string
	for _, f := range mustPlan(t, 1) {
		if f.OriginCode != "CGK" || f.DestinationCode != "DPS" {
			continue
		}
		if len(firstFour) < 4 {
			firstFour = append(firstFour, f.FlightNumber)
		}
		lastOfDay = []string{f.FlightNumber}
	}

	require.Equal(t, []string{"GA100", "QG100", "JT100", "ID100"}, firstFour, "each airline starts at the route base")
	require.Equal(t, []string{"QZ149"}, lastOfDay, "300 slots over six airlines give 50 numbers each")
}

func TestTimeOfDayFactor_changesExactlyAtTheEdgesOfPeakAndNight(t *testing.T) {
	tests := []struct {
		hour int
		want float64
	}{
		{0, 0.85}, {4, 0.85}, {5, 1.0},
		{6, 1.25}, {8, 1.25}, {9, 1.0},
		{15, 1.0}, {16, 1.25}, {19, 1.25}, {20, 1.0},
		{21, 1.0}, {22, 0.85}, {23, 0.85},
	}
	for _, tt := range tests {
		got := timeOfDayFactor(time.Duration(tt.hour)*time.Hour + 59*time.Minute)
		require.Equal(t, tt.want, got, "hour %d", tt.hour)
	}
}
