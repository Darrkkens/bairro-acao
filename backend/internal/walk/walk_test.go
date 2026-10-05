package walk

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeNeighborhood(t *testing.T) {
	got, err := NormalizeNeighborhood("  Vila \t Mariana\n")
	if err != nil || got != "Vila Mariana" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, bad := range []string{"", " a ", strings.Repeat("x", 81)} {
		if _, err := NormalizeNeighborhood(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestNormalizeID(t *testing.T) {
	if id, ok := NormalizeID(" 6947CACD-6C34-4D0D-A59B-D0B41457D946 "); !ok || id != "6947cacd-6c34-4d0d-a59b-d0b41457d946" {
		t.Fatalf("got %q %v", id, ok)
	}
	for _, bad := range []string{"", "../etc/passwd", "6947cacd6c344d0da59bd0b41457d946"} {
		if _, ok := NormalizeID(bad); ok {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestNormalizeNoteKeepsParagraphs(t *testing.T) {
	got, err := NormalizeNote("  buraco   fundo\r\n\r\n\r\n\r\nperto da   escola ")
	if err != nil || got != "buraco fundo\n\nperto da escola" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestNormalizeReview(t *testing.T) {
	r, err := NormalizeReview(Review{Category: Street, Title: " Buraco\nna rua ", Description: " fundo "})
	if err != nil || r.Title != "Buraco na rua" || r.Description != "fundo" {
		t.Fatalf("got %+v, %v", r, err)
	}
	if _, err := NormalizeReview(Review{Category: "parques", Title: "x"}); err == nil {
		t.Error("unknown category accepted")
	}
	if _, err := NormalizeReview(Review{Category: Other, Title: "  "}); err == nil {
		t.Error("empty title accepted")
	}
}

func TestValidateTime(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	if err := ValidateTime(now.Add(-72*time.Hour), now); err != nil {
		t.Errorf("a walk synced days later must be accepted: %v", err)
	}
	if err := ValidateTime(now.Add(48*time.Hour), now); err == nil {
		t.Error("clock far in the future accepted")
	}
	if err := ValidateTime(time.Time{}, now); err == nil {
		t.Error("zero time accepted")
	}
}

func TestValidateLocation(t *testing.T) {
	accuracy := 8.0
	if err := ValidateLocation(&Location{Latitude: -23.58, Longitude: -46.63, Accuracy: &accuracy}); err != nil {
		t.Error(err)
	}
	if err := ValidateLocation(&Location{Latitude: 91, Longitude: 0}); err == nil {
		t.Error("latitude out of range accepted")
	}
}

func TestPhotoKind(t *testing.T) {
	if kind, ok := PhotoKind([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0}); !ok || kind != "jpg" {
		t.Errorf("jpeg: %q %v", kind, ok)
	}
	if kind, ok := PhotoKind([]byte("\x89PNG\r\n\x1a\n...")); !ok || kind != "png" {
		t.Errorf("png: %q %v", kind, ok)
	}
	if _, ok := PhotoKind([]byte("<svg onload=alert(1)>")); ok {
		t.Error("svg accepted")
	}
}

func TestNormalizeCityAndState(t *testing.T) {
	if city, err := NormalizeCity("  São   Paulo "); err != nil || city != "São Paulo" {
		t.Fatalf("got %q, %v", city, err)
	}
	if uf, err := NormalizeState(" sp "); err != nil || uf != "SP" {
		t.Fatalf("got %q, %v", uf, err)
	}
	for _, bad := range []string{"", "XX", "São Paulo"} {
		if _, err := NormalizeState(bad); err == nil {
			t.Errorf("state %q accepted", bad)
		}
	}
}

func TestCleanTrackDropsBadPointsAndSorts(t *testing.T) {
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	got := CleanTrack([]TrackPoint{
		{Latitude: -23.58, Longitude: -46.63, RecordedAt: now.Add(-time.Minute)},
		{Latitude: 123, Longitude: -46.63, RecordedAt: now},
		{Latitude: -23.59, Longitude: -46.64, RecordedAt: now.Add(-2 * time.Minute)},
		{Latitude: -23.59, Longitude: -46.64},
	}, now)
	if len(got) != 2 || !got[0].RecordedAt.Before(got[1].RecordedAt) {
		t.Fatalf("got %+v", got)
	}
}

func TestRouteMergesPhotosAndMeasuresDistance(t *testing.T) {
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	track := []TrackPoint{{Latitude: -23.5880, Longitude: -46.6340, RecordedAt: start}, {Latitude: -23.5880, Longitude: -46.6300, RecordedAt: start.Add(4 * time.Minute)}}
	photo := Occurrence{CapturedAt: start.Add(2 * time.Minute), Location: &Location{Latitude: -23.5880, Longitude: -46.6320}}
	route := Route(track, []Occurrence{photo, {CapturedAt: start}})
	if len(route) != 3 || route[1].Longitude != -46.6320 {
		t.Fatalf("got %+v", route)
	}
	// 0.004° of longitude at this latitude is about 408 m.
	if d := RouteLength(route); d < 400 || d > 415 {
		t.Fatalf("distance %.1f m", d)
	}
}
