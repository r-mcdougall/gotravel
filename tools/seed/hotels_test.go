package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// knownCities are the cities the catalog migration creates, with their names.
var knownCities = map[string]string{
	"JKT": "Jakarta", "SUB": "Surabaya", "MES": "Medan", "JOG": "Yogyakarta",
	"DPS": "Denpasar", "UPG": "Makassar", "BPN": "Balikpapan", "DJJ": "Jayapura",
	"SIN": "Singapore", "KUL": "Kuala Lumpur", "TYO": "Tokyo",
}

const hotelsPerSeed = 33 // three hotels in each of the eleven cities

func TestPlanHotels_givesEveryCityThreeHotelsWithDistinctNames(t *testing.T) {
	hotels := planHotels()

	require.Len(t, hotels, hotelsPerSeed)
	perCity := map[string][]string{}
	for _, h := range hotels {
		perCity[h.CityCode] = append(perCity[h.CityCode], h.Name)
	}
	require.Len(t, perCity, len(knownCities))
	for code, names := range perCity {
		require.Len(t, names, 3, code)
		require.Equal(t, []string{
			knownCities[code] + " Budget Inn", knownCities[code] + " Central Hotel", knownCities[code] + " Grand Resort",
		}, names, "the catalog refuses two hotels with one name in a city")
	}
}

func TestPlanHotels_buildsHotelsTheCatalogAccepts(t *testing.T) {
	for _, h := range planHotels() {
		require.Contains(t, knownCities, h.CityCode, h.Name)
		require.NotEmpty(t, h.Name)
		require.LessOrEqual(t, utf8.RuneCountInString(h.Name), 100, h.Name)
		require.NotEmpty(t, h.Address, h.Name)
		require.LessOrEqual(t, utf8.RuneCountInString(h.Address), 200, h.Name)
		require.GreaterOrEqual(t, h.StarRating, int32(1), h.Name)
		require.LessOrEqual(t, h.StarRating, int32(5), h.Name)
		require.NotEmpty(t, h.RoomTypes, h.Name)
		require.LessOrEqual(t, len(h.RoomTypes), 10, h.Name)

		names := map[string]bool{}
		for _, room := range h.RoomTypes {
			require.False(t, names[room.Name], "%s has two %s room types", h.Name, room.Name)
			names[room.Name] = true
			require.GreaterOrEqual(t, room.MaxGuests, int32(1), h.Name)
			require.LessOrEqual(t, room.MaxGuests, int32(10), h.Name)
			require.GreaterOrEqual(t, room.TotalRooms, int32(1), h.Name)
			require.LessOrEqual(t, room.TotalRooms, int32(1000), h.Name)
			require.Zero(t, room.PricePerNight%priceStep, "%s %s costs %d", h.Name, room.Name, room.PricePerNight)
			require.GreaterOrEqual(t, room.PricePerNight, int64(150_000), "%s %s", h.Name, room.Name)
		}
	}
}

func TestPlanHotels_pricesRoomsFromTheCityBasePrice(t *testing.T) {
	prices := map[string]int64{}
	for _, h := range planHotels() {
		for _, room := range h.RoomTypes {
			prices[h.Name+" / "+room.Name] = room.PricePerNight
		}
	}

	require.Equal(t, int64(280_000), prices["Jakarta Budget Inn / Standard Single"], "0.35 of 800,000")
	require.Equal(t, int64(385_000), prices["Medan Central Hotel / Superior Double"], "0.7 of 550,000, already a step of 5,000")
	require.Equal(t, int64(195_000), prices["Yogyakarta Budget Inn / Standard Single"], "0.35 of 550,000 is 192,500, a half step rounded up")
	require.Equal(t, int64(250_000), prices["Medan Budget Inn / Standard Twin"], "0.45 of 550,000 is 247,500, a half step rounded up")
	require.Equal(t, int64(2_700_000), prices["Denpasar Grand Resort / Family Suite"], "3.0 of 900,000")
}

func TestPlanHotels_hasARoomForUpToFourGuestsInEveryCityAndDearerRoomsInBetterHotels(t *testing.T) {
	largest := map[string]int32{}
	cheapest := map[string][]int64{}
	for _, h := range planHotels() {
		lowest := h.RoomTypes[0].PricePerNight
		for _, room := range h.RoomTypes {
			largest[h.CityCode] = max(largest[h.CityCode], room.MaxGuests)
			lowest = min(lowest, room.PricePerNight)
		}
		cheapest[h.CityCode] = append(cheapest[h.CityCode], lowest)
	}

	for code := range knownCities {
		require.Equal(t, int32(4), largest[code], code)
		budget, central, resort := cheapest[code][0], cheapest[code][1], cheapest[code][2]
		require.Less(t, budget, central, code)
		require.Less(t, central, resort, code)
	}
}

func TestPlanHotels_isTheSameEveryTime(t *testing.T) {
	first, err := json.Marshal(planHotels())
	require.NoError(t, err)
	second, err := json.Marshal(planHotels())
	require.NoError(t, err)

	require.JSONEq(t, string(first), string(second))
}

// fakeHotelCreator answers each hotel with the function it was given and records every call.
type fakeHotelCreator struct {
	answer func(call int, spec hotelSpec) (outcome, error)

	mu    sync.Mutex
	calls []hotelSpec
}

func (f *fakeHotelCreator) createHotel(_ context.Context, spec hotelSpec) (outcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	call := len(f.calls)
	f.mu.Unlock()
	return f.answer(call, spec)
}

func TestSeedHotels_triesEveryHotelOnceAndCountsWhatHappened(t *testing.T) {
	var logs bytes.Buffer
	creator := &fakeHotelCreator{answer: func(call int, _ hotelSpec) (outcome, error) {
		switch call {
		case 2, 5:
			return outcomeExisting, nil
		case 7:
			return 0, errors.New("hotel seven broke")
		default:
			return outcomeCreated, nil
		}
	}}
	hotels := planHotels()

	got := seedHotels(context.Background(), creator, hotels, slog.New(slog.NewTextHandler(&logs, nil)))

	require.Equal(t, summary{created: hotelsPerSeed - 3, existing: 2, failed: 1}, got)
	require.Equal(t, hotels, creator.calls, "in plan order, each once")
	require.Contains(t, logs.String(), "create hotel failed")
	require.Contains(t, logs.String(), "hotel seven broke")
}

func TestSeedHotels_keepsGoingAfterFailures(t *testing.T) {
	creator := &fakeHotelCreator{answer: func(int, hotelSpec) (outcome, error) { return 0, errors.New("boom") }}

	got := seedHotels(context.Background(), creator, planHotels(), quietLogger())

	require.Equal(t, summary{failed: hotelsPerSeed}, got, "33 hotels are few enough to try them all")
}

func TestSeedHotels_stopsQuietlyWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	creator := &fakeHotelCreator{answer: func(call int, _ hotelSpec) (outcome, error) {
		if call == 3 {
			cancel()
			return 0, ctx.Err()
		}
		return outcomeCreated, nil
	}}

	got := seedHotels(ctx, creator, planHotels(), quietLogger())

	require.Equal(t, summary{created: 2}, got, "the cut-short hotel is not a failure")
	require.Len(t, creator.calls, 3)
}

func TestSeedHotels_aCancelledErrorFromTheServerIsStillAFailure(t *testing.T) {
	creator := &fakeHotelCreator{answer: func(call int, _ hotelSpec) (outcome, error) {
		if call == 2 {
			return 0, context.Canceled // not our doing: the context is alive
		}
		return outcomeCreated, nil
	}}

	got := seedHotels(context.Background(), creator, planHotels()[:3], quietLogger())

	require.Equal(t, summary{created: 2, failed: 1}, got)
}

func TestSeedHotels_doesNothingWithoutHotels(t *testing.T) {
	got := seedHotels(context.Background(), &fakeHotelCreator{}, nil, quietLogger())

	require.Equal(t, summary{}, got)
}
