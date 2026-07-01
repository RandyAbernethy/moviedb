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

func TestNewStoreCreatesEmptyDatabaseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.json")

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}

	if store.Count() != 0 {
		t.Fatalf("expected new store to be empty, got %d movies", store.Count())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "[]" {
		t.Fatalf("expected empty movies.json to be created, got %q", data)
	}
}

func TestStoreSaveKeepsMemoryUnchangedWhenFlushFails(t *testing.T) {
	dir := t.TempDir()
	store := &Store{path: filepath.Join(dir, "missing", "movies.json"), movies: map[string]Movie{
		"existing": {ID: "existing", Title: "Existing", CreatedAt: time.Unix(1, 0)},
	}}

	if err := store.Save(Movie{ID: "new", Title: "New"}); err == nil {
		t.Fatal("expected save to fail when the database directory is missing")
	}
	if _, ok := store.Get("new"); ok {
		t.Fatal("failed save should not be visible in memory")
	}
	if got, ok := store.Get("existing"); !ok || got.Title != "Existing" {
		t.Fatalf("existing movie should remain unchanged, got %+v", got)
	}
}

func TestReplaceFileWithRetryRetriesTransientFailure(t *testing.T) {
	attempts := 0
	sleeps := 0
	err := replaceFileWithRetryFunc("movies.json.tmp", "movies.json", func(oldPath, newPath string) error {
		attempts++
		if oldPath != "movies.json.tmp" || newPath != "movies.json" {
			t.Fatalf("unexpected rename paths %q -> %q", oldPath, newPath)
		}
		if attempts < 3 {
			return os.ErrPermission
		}
		return nil
	}, func(time.Duration) {
		sleeps++
	})

	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("expected three rename attempts, got %d", attempts)
	}
	if sleeps != 2 {
		t.Fatalf("expected two retry sleeps, got %d", sleeps)
	}
}

func TestWriteMoviesFileCreatesBackupOfPreviousMovies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.json")
	previous := []Movie{{ID: "old", Title: "Old", CreatedAt: time.Unix(1, 0)}}
	next := []Movie{{ID: "new", Title: "New", CreatedAt: time.Unix(2, 0)}}

	if err := writeMoviesFile(path, previous, nil); err != nil {
		t.Fatal(err)
	}
	if err := writeMoviesFile(path, next, previous); err != nil {
		t.Fatal(err)
	}

	got, err := readMoviesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("expected primary database to contain new movie, got %+v", got)
	}
	backup, err := readMoviesFile(backupMoviesPath(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(backup) != 1 || backup[0].ID != "old" {
		t.Fatalf("expected backup database to contain previous movie, got %+v", backup)
	}
}

func TestLoadRecoversFromBackupWhenPrimaryIsUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	backup := []Movie{{ID: "safe", Title: "Safe", CreatedAt: time.Unix(1, 0)}}
	if err := writeMoviesFile(backupMoviesPath(path), backup, nil); err != nil {
		t.Fatal(err)
	}
	store := &Store{path: path, movies: map[string]Movie{}}

	if err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if got, ok := store.Get("safe"); !ok || got.Title != "Safe" {
		t.Fatalf("expected movie to be loaded from backup, got %+v", got)
	}
	restored, err := readMoviesFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0].ID != "safe" {
		t.Fatalf("expected primary database to be restored from backup, got %+v", restored)
	}
}

func TestNewStoreRecoversFromBackupWhenPrimaryIsMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "movies.json")
	backup := []Movie{{ID: "safe", Title: "Safe", CreatedAt: time.Unix(1, 0)}}
	if err := writeMoviesFile(backupMoviesPath(path), backup, nil); err != nil {
		t.Fatal(err)
	}

	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if store.Count() != 1 {
		t.Fatalf("expected store to recover one movie from backup, got %d", store.Count())
	}
	if got, ok := store.Get("safe"); !ok || got.Title != "Safe" {
		t.Fatalf("expected movie to be loaded from backup, got %+v", got)
	}
	if _, err := readMoviesFile(path); err != nil {
		t.Fatalf("expected primary database to be restored, got %v", err)
	}
}

func TestImageHandlerRejectsDirectoryListing(t *testing.T) {
	server := &Server{imageDir: t.TempDir(), imageBase: "/images/"}
	req := httptest.NewRequest(http.MethodGet, "/images/", nil)
	rec := httptest.NewRecorder()

	server.handleImageFile(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected image directory listing to be hidden, got %d", rec.Code)
	}
}

func TestSaveUploadedImageRejectsSVG(t *testing.T) {
	server := &Server{imageDir: t.TempDir(), imageBase: "/images/"}
	movie := Movie{ID: "movie-id"}

	if _, err := server.saveUploadedImage(&movie, strings.NewReader(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`), "cover.svg"); err == nil {
		t.Fatal("expected SVG upload to be rejected")
	}
}

func TestSaveUploadedImageAcceptsJPEG(t *testing.T) {
	server := &Server{imageDir: t.TempDir(), imageBase: "/images/"}
	movie := Movie{ID: "movie-id"}
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}

	path, err := server.saveUploadedImage(&movie, bytes.NewReader(jpeg), "cover.jfif")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path, "/images/movie-id-cover-") || !strings.HasSuffix(path, ".jpg") {
		t.Fatalf("expected normalized local JPEG path, got %q", path)
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

func TestManualMovieRejectsUnsafeImagePath(t *testing.T) {
	server := newTestServer(t)
	rec := postManualMovie(t, server, Movie{Title: "Unsafe Poster", ImagePath: "https://example.com/poster.jpg"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected unsafe image path to be rejected, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMoviesPostRejectsOversizedJSON(t *testing.T) {
	server := newTestServer(t)
	hugeTitle := strings.Repeat("a", maxJSONBodyBytes)
	body := []byte(`{"titles":["` + hugeTitle + `"]}`)

	req := httptest.NewRequest(http.MethodPost, "/api/movies", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleMovies(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized JSON to be rejected, got %d: %s", rec.Code, rec.Body.String())
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

func TestMergeDuplicatesPrefersNewerUpdatedAt(t *testing.T) {
	store := &Store{path: filepath.Join(t.TempDir(), "movies.json"), movies: map[string]Movie{
		"old": {
			ID:          "old",
			Title:       "Duplicate",
			ReleaseDate: "2001",
			Studio:      "Old Studio",
			CreatedAt:   time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		"new": {
			ID:          "new",
			Title:       "Duplicate",
			ReleaseDate: "2001",
			Studio:      "New Studio",
			Notes:       "newer note",
			CreatedAt:   time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC),
			UpdatedAt:   time.Date(2020, 1, 3, 0, 0, 0, 0, time.UTC),
		},
	}}

	merged, err := store.MergeDuplicates()
	if err != nil {
		t.Fatal(err)
	}
	if merged != 1 {
		t.Fatalf("expected one duplicate merge, got %d", merged)
	}
	got, ok := store.Get("old")
	if !ok {
		t.Fatal("expected older ID to be retained")
	}
	if got.Studio != "New Studio" || got.Notes != "newer note" {
		t.Fatalf("expected newer fields to win, got %+v", got)
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

func TestHostPolicyAllowsWildcardLANIPsOnly(t *testing.T) {
	policy := newHostPolicy([]string{"0.0.0.0"}, appPort)
	if !policy.allows("192.168.1.25:8765") {
		t.Fatal("expected private LAN IP to be allowed for wildcard bind")
	}
	if policy.allows("evil.example:8765") {
		t.Fatal("expected arbitrary hostname to be rejected")
	}
}

func TestSecurityMiddlewareRejectsUnexpectedHost(t *testing.T) {
	handler := securityMiddleware(newHostPolicy([]string{"127.0.0.1"}, appPort), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "http://evil.example/moviedb/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected unexpected host to be rejected, got %d", rec.Code)
	}
}

func TestSecurityMiddlewareRejectsCrossOriginMutation(t *testing.T) {
	handler := securityMiddleware(newHostPolicy([]string{"127.0.0.1"}, appPort), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/movies", nil)
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected cross-origin mutation to be rejected, got %d", rec.Code)
	}
}

func TestSecurityMiddlewareAllowsSameOriginMutation(t *testing.T) {
	handler := securityMiddleware(newHostPolicy([]string{"127.0.0.1"}, appPort), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8765/api/movies", nil)
	req.Header.Set("Origin", "http://127.0.0.1:8765")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected same-origin mutation to pass, got %d", rec.Code)
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

func TestDatabaseDirDefaultsToAppRootData(t *testing.T) {
	root := t.TempDir()

	got := databaseDir(root, "")

	if got != filepath.Join(root, "data") {
		t.Fatalf("expected default database directory under app root, got %q", got)
	}
}

func TestDatabaseDirUsesDBPathOverride(t *testing.T) {
	root := t.TempDir()
	override := filepath.Join(t.TempDir(), "custom-db")

	got := databaseDir(root, "  "+override+"  ")

	if got != cleanAbsPath(override) {
		t.Fatalf("expected db-path override to win, got %q", got)
	}
}

func TestLoadDotEnvIgnoresMissingFile(t *testing.T) {
	if err := loadDotEnv(filepath.Join(t.TempDir(), ".env")); err != nil {
		t.Fatalf("expected missing .env to be ignored, got %v", err)
	}
}

func TestLoadDotEnvSetsValuesWithoutOverwritingEnvironment(t *testing.T) {
	keys := []string{
		"MOVIEDB_TEST_DOTENV_A",
		"MOVIEDB_TEST_DOTENV_QUOTED",
		"MOVIEDB_TEST_DOTENV_SINGLE",
		"MOVIEDB_TEST_DOTENV_INLINE",
		"MOVIEDB_TEST_DOTENV_HASH",
		"MOVIEDB_TEST_DOTENV_EXPORTED",
		"MOVIEDB_TEST_DOTENV_EMPTY",
		"MOVIEDB_TEST_DOTENV_EXISTING",
	}
	unsetEnvForTest(t, keys...)
	if err := os.Setenv("MOVIEDB_TEST_DOTENV_EXISTING", "from-env"); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), ".env")
	content := strings.Join([]string{
		"# MovieDB local settings",
		"MOVIEDB_TEST_DOTENV_A=from-file",
		`MOVIEDB_TEST_DOTENV_QUOTED="quoted value"`,
		"MOVIEDB_TEST_DOTENV_SINGLE='single value'",
		"MOVIEDB_TEST_DOTENV_INLINE=keep this # drop this comment",
		"MOVIEDB_TEST_DOTENV_HASH=keep#hash",
		"export MOVIEDB_TEST_DOTENV_EXPORTED=exported",
		"MOVIEDB_TEST_DOTENV_EMPTY=",
		"MOVIEDB_TEST_DOTENV_EXISTING=from-file",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := loadDotEnv(path); err != nil {
		t.Fatal(err)
	}

	expected := map[string]string{
		"MOVIEDB_TEST_DOTENV_A":        "from-file",
		"MOVIEDB_TEST_DOTENV_QUOTED":   "quoted value",
		"MOVIEDB_TEST_DOTENV_SINGLE":   "single value",
		"MOVIEDB_TEST_DOTENV_INLINE":   "keep this",
		"MOVIEDB_TEST_DOTENV_HASH":     "keep#hash",
		"MOVIEDB_TEST_DOTENV_EXPORTED": "exported",
		"MOVIEDB_TEST_DOTENV_EMPTY":    "",
		"MOVIEDB_TEST_DOTENV_EXISTING": "from-env",
	}
	for key, want := range expected {
		if got := os.Getenv(key); got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestLoadDotEnvReportsMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("MOVIEDB_TEST_DOTENV_BROKEN\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := loadDotEnv(path)
	if err == nil || !strings.Contains(err.Error(), "expected KEY=VALUE") {
		t.Fatalf("expected malformed .env error, got %v", err)
	}
}

func TestValidateRemoteFetchURLRejectsUnsafeTargets(t *testing.T) {
	values := []string{
		"file:///etc/passwd",
		"http://127.0.0.1:8765/",
		"http://10.0.0.1/poster.jpg",
		"https://user:pass@example.com/poster.jpg",
	}
	for _, value := range values {
		if err := validateRemoteFetchURL(value); err == nil {
			t.Fatalf("expected %q to be rejected", value)
		}
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

func unsetEnvForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		key := key
		old, hadOld := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if hadOld {
				_ = os.Setenv(key, old)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}
