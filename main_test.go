package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDeleteMovieImageFileOnlyDeletesLocalImage(t *testing.T) {
	dir := t.TempDir()
	server := &Server{imageDir: dir, imageBase: "/images/"}
	imageName := "movie-cover.jpg"
	imagePath := filepath.Join(dir, imageName)
	if err := os.WriteFile(imagePath, []byte("image"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := server.deleteMovieImageFile("/images/" + imageName); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(imagePath); !os.IsNotExist(err) {
		t.Fatalf("expected image to be deleted, stat error was %v", err)
	}
}

func TestDeleteMovieImageFileRejectsUnsafePath(t *testing.T) {
	server := &Server{imageDir: t.TempDir(), imageBase: "/images/"}
	if err := server.deleteMovieImageFile("/images/../other.jpg"); err == nil {
		t.Fatal("expected unsafe path to be rejected")
	}
}

func TestSaveMovieWithNewImageDeletesOldImage(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "movies.json"))
	if err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldImage := filepath.Join(imageDir, "old-cover.jpg")
	if err := os.WriteFile(oldImage, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := &Server{store: store, imageDir: imageDir, imageBase: "/images/"}
	old := Movie{ID: "movie-id", Title: "Movie", ImagePath: "/images/old-cover.jpg"}
	if err := store.Save(old); err != nil {
		t.Fatal(err)
	}
	updated := old
	updated.ImagePath = "/images/new-cover.jpg"
	body, err := json.Marshal(updated)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/movies/movie-id", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleMovie(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected save to succeed, got %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(oldImage); !os.IsNotExist(err) {
		t.Fatalf("expected old image to be deleted after save, stat error was %v", err)
	}
	got, ok := store.Get("movie-id")
	if !ok || got.ImagePath != updated.ImagePath {
		t.Fatalf("expected saved movie to use new image path, got %+v", got)
	}
}

func TestManualMovieRequiresTitle(t *testing.T) {
	server := newTestServer(t)
	body := []byte(`{"movie":{"format":"DVD"}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/movies", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleMovies(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected blank title to be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestManualMovieDuplicateTitleRequiresDifferentReleaseDate(t *testing.T) {
	server := newTestServer(t)
	first := postManualMovie(t, server, Movie{Title: "Hamlet", ReleaseDate: "1996-12-25"})
	if first.Code != http.StatusOK {
		t.Fatalf("expected first movie to save, got %d: %s", first.Code, first.Body.String())
	}

	duplicate := postManualMovie(t, server, Movie{Title: "Hamlet", ReleaseDate: "1996"})
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("expected same title/year to be rejected, got %d: %s", duplicate.Code, duplicate.Body.String())
	}

	differentRelease := postManualMovie(t, server, Movie{Title: "Hamlet", ReleaseDate: "1948-05-06"})
	if differentRelease.Code != http.StatusOK {
		t.Fatalf("expected same title with different release date to save, got %d: %s", differentRelease.Code, differentRelease.Body.String())
	}
}

func TestBlankMyRatingSearchReturnsOnlyUnratedMovies(t *testing.T) {
	store := &Store{movies: map[string]Movie{
		"rated":   {ID: "rated", Title: "Rated", MyRating: "8"},
		"unrated": {ID: "unrated", Title: "Unrated"},
	}}

	results := store.All("", []string{"myRating"})
	if got := movieTitles(results); !slices.Equal(got, []string{"Unrated"}) {
		t.Fatalf("expected only unrated movie, got %v", got)
	}
}

func TestBlankAllFieldsSearchStillReturnsAllMovies(t *testing.T) {
	store := &Store{movies: map[string]Movie{
		"rated":   {ID: "rated", Title: "Rated", MyRating: "8"},
		"unrated": {ID: "unrated", Title: "Unrated"},
	}}

	results := store.All("", nil)
	if got := movieTitles(results); !slices.Equal(got, []string{"Rated", "Unrated"}) {
		t.Fatalf("expected all movies, got %v", got)
	}
}

func TestMyRatingNumericSearchIsExact(t *testing.T) {
	store := &Store{movies: map[string]Movie{
		"one": {ID: "one", Title: "One", MyRating: "1"},
		"ten": {ID: "ten", Title: "Ten", MyRating: "10"},
	}}

	oneResults := store.All("1", []string{"myRating"})
	if got := movieTitles(oneResults); !slices.Equal(got, []string{"One"}) {
		t.Fatalf("expected rating 1 only, got %v", got)
	}

	tenResults := store.All("10", []string{"myRating"})
	if got := movieTitles(tenResults); !slices.Equal(got, []string{"Ten"}) {
		t.Fatalf("expected rating 10 only, got %v", got)
	}
}

func TestHostListAllowsRepeatedHosts(t *testing.T) {
	var hosts hostList
	if err := hosts.Set("127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := hosts.Set("192.168.1.25"); err != nil {
		t.Fatal(err)
	}
	if got := hosts.String(); got != "127.0.0.1,192.168.1.25" {
		t.Fatalf("unexpected hosts string %q", got)
	}
}

func TestHostListRejectsEmptyHost(t *testing.T) {
	var hosts hostList
	if err := hosts.Set(" "); err == nil {
		t.Fatal("expected empty host to be rejected")
	}
}

func TestValidPort(t *testing.T) {
	valid := []int{1, appPort, 65535}
	for _, port := range valid {
		if !validPort(port) {
			t.Fatalf("expected port %d to be valid", port)
		}
	}
	invalid := []int{0, -1, 65536}
	for _, port := range invalid {
		if validPort(port) {
			t.Fatalf("expected port %d to be invalid", port)
		}
	}
}

func TestBrowserURLUsesMovieDBRoute(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if got := browserURLForListener(ln); !strings.HasSuffix(got, "/moviedb/") {
		t.Fatalf("expected /moviedb/ route, got %q", got)
	}
}

func TestPreserveLocalMovieFieldsKeepsUserOwnedData(t *testing.T) {
	old := Movie{
		ID:        "id",
		Format:    "Blu-ray",
		Location:  "Shelf A",
		Notes:     "My note",
		MyRating:  "9",
		AmazonURL: "https://www.amazon.com/dp/ABCDEFGHIJ",
		ImagePath: "/images/local-cover.jpg",
		CreatedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	refreshed := Movie{
		ID:        "other",
		Title:     "New Title",
		Format:    "DVD",
		Location:  "Remote",
		Notes:     "Remote note",
		MyRating:  "1",
		ImagePath: "/images/source-cover.jpg",
	}

	got := preserveLocalMovieFields(old, refreshed)
	if got.ID != old.ID || got.Format != old.Format || got.Location != old.Location ||
		got.Notes != old.Notes || got.MyRating != old.MyRating ||
		got.AmazonURL != old.AmazonURL || !got.CreatedAt.Equal(old.CreatedAt) {
		t.Fatalf("local fields were not preserved: %+v", got)
	}
	if got.ImagePath != refreshed.ImagePath {
		t.Fatalf("expected source image to replace local image, got %q", got.ImagePath)
	}
	if got.Title != refreshed.Title {
		t.Fatalf("source title was not refreshed: %+v", got)
	}
}

func TestPreserveLocalMovieFieldsKeepsLocalImageWhenSourceHasNone(t *testing.T) {
	old := Movie{
		ID:        "id",
		ImagePath: "/images/local-cover.jpg",
		CreatedAt: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	refreshed := Movie{
		ID:    "other",
		Title: "New Title",
	}

	got := preserveLocalMovieFields(old, refreshed)
	if got.ImagePath != old.ImagePath {
		t.Fatalf("expected local image to survive when source has no image, got %q", got.ImagePath)
	}
}

func movieTitles(movies []Movie) []string {
	titles := make([]string, 0, len(movies))
	for _, movie := range movies {
		titles = append(titles, movie.Title)
	}
	return titles
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := NewStore(filepath.Join(dir, "movies.json"))
	if err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(dir, "images")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &Server{store: store, imageDir: imageDir, imageBase: "/images/"}
}

func postManualMovie(t *testing.T, server *Server, movie Movie) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"movie": movie})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/movies", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleMovies(rec, req)
	return rec
}
