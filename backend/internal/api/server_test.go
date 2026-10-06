package api

import (
	"bairroacao/internal/photos"
	"bairroacao/internal/store"
	"bairroacao/internal/walk"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type aiStub struct{}

func (aiStub) Model() string                { return "stub" }
func (aiStub) Healthy(context.Context) bool { return true }

// newServer runs against TEST_DATABASE_URL inside a throwaway schema.
func newServer(t *testing.T) (http.Handler, *store.Postgres, *int) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run API tests against PostgreSQL")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close(context.Background())
	})
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	db, err := store.OpenPostgres(ctx, url+sep+"search_path="+schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	dir, err := photos.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wakes := new(int)
	service := &walk.Service{Repo: db, Photos: dir, Wake: func() { *wakes++ }}
	return New(Config{Walks: service, Photos: dir, AI: aiStub{}, Database: db, Location: time.UTC}), db, wakes
}

func do(t *testing.T, h http.Handler, method, path, contentType string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, body)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func upload(t *testing.T, h http.Handler, walkID, id string, photo []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("photo", "p.jpg")
	_, _ = part.Write(photo)
	for k, v := range fields {
		_ = form.WriteField(k, v)
	}
	_ = form.Close()
	return do(t, h, http.MethodPut, "/api/walks/"+walkID+"/occurrences/"+id, form.FormDataContentType(), &body)
}

func TestWalkLifecycle(t *testing.T) {
	h, db, wakes := newServer(t)
	ctx := context.Background()
	walkID := "0b7f3c1e-9a4d-4f5e-8c2b-1d3e5f7a9b0c"
	occurrenceID := "4a6c8e0f-2b4d-4f6a-8c0e-2a4c6e8f0b2d"
	now := time.Now().UTC().Format(time.RFC3339)
	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{7}, 64)...)

	start := `{"neighborhood":"  Bom   Retiro ","city":"São Paulo","state":"sp","started_at":"` + now + `"}`
	if rec := do(t, h, http.MethodPut, "/api/walks/"+walkID, "application/json", strings.NewReader(start)); rec.Code != http.StatusCreated {
		t.Fatalf("start walk: %d %s", rec.Code, rec.Body)
	}
	if rec := do(t, h, http.MethodPut, "/api/walks/"+walkID, "application/json", strings.NewReader(start)); rec.Code != http.StatusOK {
		t.Fatalf("retrying the same walk must be idempotent: %d", rec.Code)
	}

	fields := map[string]string{"note": "banco sem assento", "captured_at": now, "latitude": "-23.5275", "longitude": "-46.6390", "accuracy_m": "7"}
	if rec := upload(t, h, walkID, occurrenceID, []byte("<svg/>"), fields); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-image upload: %d", rec.Code)
	}
	if rec := upload(t, h, walkID, occurrenceID, jpeg, fields); rec.Code != http.StatusCreated {
		t.Fatalf("record: %d %s", rec.Code, rec.Body)
	}
	if rec := upload(t, h, walkID, occurrenceID, jpeg, fields); rec.Code != http.StatusOK || *wakes != 1 {
		t.Fatalf("retried upload: %d, wakes %d", rec.Code, *wakes)
	}
	if rec := upload(t, h, "9f8e7d6c-5b4a-4392-8170-6f5e4d3c2b1a", "1c2d3e4f-5a6b-4c7d-8e9f-0a1b2c3d4e5f", jpeg, fields); rec.Code != http.StatusNotFound {
		t.Fatalf("upload to unknown walk: %d", rec.Code)
	}

	// The worker claims the occurrence and stores the AI's reading.
	job, ok, err := db.ClaimNext(ctx)
	if err != nil || !ok || job.ID != occurrenceID || job.Neighborhood != "Bom Retiro" || job.Note != "banco sem assento" {
		t.Fatalf("claim: %+v %v %v", job, ok, err)
	}
	if err := db.CompleteAnalysis(ctx, job.ID, walk.AIDone, walk.Suggestion{Category: walk.Other, Title: "Banco danificado", Description: "Banco sem assento.", Confidence: "media", Model: "stub"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	review := `{"category":"lazer","title":"Banco sem assento na praça","description":"Assento removido."}`
	if rec := do(t, h, http.MethodPatch, "/api/occurrences/"+occurrenceID, "application/json", strings.NewReader(review)); rec.Code != http.StatusOK {
		t.Fatalf("review: %d %s", rec.Code, rec.Body)
	}
	t0 := time.Now().UTC().Add(-10 * time.Minute)
	finish := fmt.Sprintf(`{"track":[{"latitude":-23.5270,"longitude":-46.6395,"accuracy_m":6,"recorded_at":%q},{"latitude":-23.5280,"longitude":-46.6385,"recorded_at":%q},{"latitude":999,"longitude":0,"recorded_at":%q}]}`,
		t0.Format(time.RFC3339), t0.Add(time.Minute).Format(time.RFC3339), t0.Add(2*time.Minute).Format(time.RFC3339))
	for range 2 { // a retried finish must not duplicate the route
		if rec := do(t, h, http.MethodPost, "/api/walks/"+walkID+"/finish", "application/json", strings.NewReader(finish)); rec.Code != http.StatusOK {
			t.Fatalf("finish: %d %s", rec.Code, rec.Body)
		}
	}
	var track []walk.TrackPoint
	if rec := do(t, h, http.MethodGet, "/api/walks/"+walkID+"/track", "", nil); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &track) != nil || len(track) != 2 || *track[0].Accuracy != 6 {
		t.Fatalf("track: %d %s", rec.Code, rec.Body)
	}

	rec := do(t, h, http.MethodGet, "/api/walks/"+walkID, "", nil)
	var got struct {
		walk.Walk
		Occurrences []walk.Occurrence `json:"occurrences"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("get walk: %d %s", rec.Code, rec.Body)
	}
	o := got.Occurrences[0]
	if got.City != "São Paulo" || got.State != "SP" || got.FinishedAt == nil || len(got.Occurrences) != 1 || o.AI == nil || o.AI.Category != walk.Other || o.Category != walk.Leisure || o.ReviewedAt == nil || o.Location == nil || *o.Location.Accuracy != 7 {
		t.Fatalf("unexpected walk %+v / %+v", got.Walk, o)
	}

	if rec := do(t, h, http.MethodGet, "/api/photos/"+o.Photo, "", nil); rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), jpeg) {
		t.Fatalf("photo: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/photos/..%2F..%2Fetc%2Fpasswd", "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("path traversal: %d", rec.Code)
	}

	rec = do(t, h, http.MethodGet, "/api/walks/"+walkID+"/report.html?download", "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Banco sem assento na praça") || !strings.Contains(rec.Body.String(), "<polyline") || !strings.Contains(rec.Header().Get("Content-Disposition"), `attachment; filename=bairro-em-acao-bom-retiro-`) {
		t.Fatalf("report: %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}

	reanalyze := `{"note":"o assento foi arrancado"}`
	if rec := do(t, h, http.MethodPost, "/api/occurrences/"+occurrenceID+"/analyze", "application/json", strings.NewReader(reanalyze)); rec.Code != http.StatusAccepted || *wakes != 2 {
		t.Fatalf("reanalyze: %d wakes %d", rec.Code, *wakes)
	}
	if rec := do(t, h, http.MethodDelete, "/api/occurrences/"+occurrenceID, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodGet, "/api/photos/"+o.Photo, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("photo after delete: %d", rec.Code)
	}
}

func TestRejectsBadInput(t *testing.T) {
	h, _, _ := newServer(t)
	cases := []struct{ method, path, contentType, body string }{
		{http.MethodPut, "/api/walks/not-a-uuid", "application/json", `{"neighborhood":"Centro","city":"São Paulo","state":"SP","started_at":"2026-10-05T10:00:00Z"}`},
		{http.MethodPut, "/api/walks/0b7f3c1e-9a4d-4f5e-8c2b-1d3e5f7a9b0c", "application/json", `{"neighborhood":"C","city":"São Paulo","state":"SP","started_at":"2026-10-05T10:00:00Z"}`},
		{http.MethodPut, "/api/walks/0b7f3c1e-9a4d-4f5e-8c2b-1d3e5f7a9b0c", "application/json", `{"neighborhood":"Centro","city":"São Paulo","state":"XX","started_at":"2026-10-05T10:00:00Z"}`},
		{http.MethodPut, "/api/walks/0b7f3c1e-9a4d-4f5e-8c2b-1d3e5f7a9b0c", "application/json", `{"neighborhood":"Centro","city":"","state":"SP","started_at":"2026-10-05T10:00:00Z"}`},
		{http.MethodPut, "/api/walks/0b7f3c1e-9a4d-4f5e-8c2b-1d3e5f7a9b0c", "application/json", `{"neighborhood":"Centro","extra":1}`},
		{http.MethodPut, "/api/walks/0b7f3c1e-9a4d-4f5e-8c2b-1d3e5f7a9b0c", "text/plain", `{}`},
	}
	for _, c := range cases {
		if rec := do(t, h, c.method, c.path, c.contentType, strings.NewReader(c.body)); rec.Code < 400 || rec.Code >= 500 {
			t.Errorf("%s %s %s: got %d", c.method, c.path, c.body, rec.Code)
		}
	}
}

func testJPEG(t *testing.T, seed int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	for y := range 240 {
		for x := range 320 {
			v := uint8((x*seed + y*7) % 200)
			img.Set(x, y, color.RGBA{v, uint8(255 - v), uint8(seed * 30), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRepeatedPhotoIsGroupedWithoutAnalysis(t *testing.T) {
	h, _, wakes := newServer(t)
	walkID, other := "6a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d", "7b2c3d4e-5f6a-4b7c-8d9e-0f1a2b3c4d5e"
	first, second := "11111111-2222-4333-8444-555555555555", "22222222-3333-4444-8555-666666666666"
	now := time.Now().UTC().Format(time.RFC3339)
	for _, id := range []string{walkID, other} {
		body := `{"neighborhood":"Centro","city":"Joaçaba","state":"SC","started_at":"` + now + `"}`
		if rec := do(t, h, http.MethodPut, "/api/walks/"+id, "application/json", strings.NewReader(body)); rec.Code != http.StatusCreated {
			t.Fatalf("walk: %d %s", rec.Code, rec.Body)
		}
	}
	photo := testJPEG(t, 3)
	fields := map[string]string{"note": "Calçada quebrada", "captured_at": now, "latitude": "-27.16776", "longitude": "-51.51792"}
	if rec := upload(t, h, walkID, first, photo, fields); rec.Code != http.StatusCreated {
		t.Fatalf("first: %d %s", rec.Code, rec.Body)
	}
	rec := upload(t, h, walkID, second, photo, fields)
	var repeated walk.Occurrence
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &repeated) != nil {
		t.Fatalf("second: %d %s", rec.Code, rec.Body)
	}
	if repeated.AIStatus != walk.AIGrouped || repeated.GroupID != first || !repeated.Duplicate || *wakes != 1 {
		t.Fatalf("the repeated photo must join the first point without waking the AI: %+v, wakes %d", repeated, *wakes)
	}

	occurrence := func(id string) walk.Occurrence {
		var got struct {
			Occurrences []walk.Occurrence `json:"occurrences"`
		}
		rec := do(t, h, http.MethodGet, "/api/walks/"+walkID, "", nil)
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		for _, o := range got.Occurrences {
			if o.ID == id {
				return o
			}
		}
		t.Fatalf("%s missing", id)
		return walk.Occurrence{}
	}

	if rec := do(t, h, http.MethodPost, "/api/occurrences/"+second+"/ungroup", "", nil); rec.Code != http.StatusNoContent || *wakes != 2 {
		t.Fatalf("ungroup: %d wakes %d", rec.Code, *wakes)
	}
	if o := occurrence(second); o.GroupID != "" || o.AIStatus != walk.AIPending {
		t.Fatalf("after ungroup: %+v", o)
	}
	if rec := do(t, h, http.MethodPost, "/api/occurrences/"+second+"/group", "application/json", strings.NewReader(`{"with":"`+first+`"}`)); rec.Code != http.StatusNoContent {
		t.Fatalf("group: %d %s", rec.Code, rec.Body)
	}
	if o := occurrence(second); o.GroupID != first || o.AIStatus != walk.AIGrouped {
		t.Fatalf("after group: %+v", o)
	}

	// A point from another walk cannot be grouped in.
	elsewhere := "33333333-4444-4555-8666-777777777777"
	if rec := upload(t, h, other, elsewhere, testJPEG(t, 5), fields); rec.Code != http.StatusCreated {
		t.Fatalf("other walk: %d", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/occurrences/"+elsewhere+"/group", "application/json", strings.NewReader(`{"with":"`+first+`"}`)); rec.Code != http.StatusBadRequest {
		t.Fatalf("cross-walk group: %d", rec.Code)
	}

	// Deleting the point removes its extra photos too.
	if rec := do(t, h, http.MethodDelete, "/api/occurrences/"+first, "", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", rec.Code)
	}
	for _, id := range []string{first, second} {
		if rec := do(t, h, http.MethodGet, "/api/photos/"+id+".jpg", "", nil); rec.Code != http.StatusNotFound {
			t.Errorf("photo %s still served after delete: %d", id, rec.Code)
		}
	}
}
