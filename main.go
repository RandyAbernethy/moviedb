package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

const appPort = 8765

const (
	maxJSONBodyBytes      = 1 << 20
	maxImageBytes         = 20 << 20
	maxMultipartBodyBytes = maxImageBytes + (1 << 20)
)

var allowedImageContentTypes = map[string]string{
	"image/gif":  ".gif",
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/webp": ".webp",
}

var allowedImageExtensions = map[string]bool{
	".gif":  true,
	".jfif": true,
	".jpeg": true,
	".jpg":  true,
	".png":  true,
	".webp": true,
}

var (
	yearInParensOrStandaloneRE = regexp.MustCompile(`\(\d{4}\)|\b\d{4}\b`)
	nonAlnumLowerRE            = regexp.MustCompile(`[^a-z0-9]+`)
	releaseYearRE              = regexp.MustCompile(`\b(18|19|20|21)\d{2}\b`)
	asinPathRE                 = regexp.MustCompile(`/(?:dp|gp/product)/([A-Z0-9]{10})`)
	whitespaceRE               = regexp.MustCompile(`\s+`)
	parentheticalRE            = regexp.MustCompile(`\([^)]*\)`)
	nonAlnumTitleRE            = regexp.MustCompile(`[^A-Za-z0-9]+`)
)

// Domain models

type Movie struct {
	ID          string            `json:"id"`
	Title       string            `json:"title"`
	Format      string            `json:"format"`
	Studio      string            `json:"studio"`
	Directors   []string          `json:"directors"`
	Cast        []string          `json:"cast"`
	Producers   []string          `json:"producers"`
	Credits     map[string]string `json:"credits"`
	Genre       []string          `json:"genre"`
	ReleaseDate string            `json:"releaseDate"`
	Runtime     string            `json:"runtime"`
	Rating      string            `json:"rating"`
	MyRating    string            `json:"myRating"`
	Synopsis    string            `json:"synopsis"`
	SourceURL   string            `json:"sourceUrl"`
	AmazonURL   string            `json:"amazonUrl"`
	ImagePath   string            `json:"imagePath"`
	Location    string            `json:"location"`
	Notes       string            `json:"notes"`
	ExternalIDs map[string]string `json:"externalIds"`
	CreatedAt   time.Time         `json:"createdAt"`
	UpdatedAt   time.Time         `json:"updatedAt"`
}

type LookupCandidate struct {
	Movie       Movie  `json:"movie"`
	MatchType   string `json:"matchType"`
	Provider    string `json:"provider"`
	Description string `json:"description"`
}

type Store struct {
	mu     sync.RWMutex
	path   string
	movies map[string]Movie
}

type hostList []string

func (h *hostList) String() string {
	return strings.Join(*h, ",")
}

func (h *hostList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("host cannot be empty")
	}
	*h = append(*h, value)
	return nil
}

type duplicatePolicy string

const (
	duplicateCancel    duplicatePolicy = "cancel"
	duplicateMergeNew  duplicatePolicy = "merge_new"
	duplicateMergeOld  duplicatePolicy = "merge_old"
	duplicateOverwrite duplicatePolicy = "overwrite"
)

// Store

func NewStore(path string) (*Store, error) {
	s := &Store{path: path, movies: map[string]Movie{}}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if !fileExists(path) {
		if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
			return nil, err
		}
	}
	if err := s.Load(); err != nil {
		return nil, err
	}
	if merged, err := s.MergeDuplicates(); err != nil {
		return nil, err
	} else if merged > 0 {
		log.Printf("merged %d duplicate movie record(s) on startup", merged)
	}
	return s, nil
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	var movies []Movie
	if err := json.NewDecoder(f).Decode(&movies); err != nil {
		return err
	}
	for _, m := range movies {
		if m.ID != "" {
			s.movies[m.ID] = m
		}
	}
	return nil
}

func (s *Store) MergeDuplicates() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	merged := 0
	for {
		var left, right Movie
		found := false
		for _, candidate := range s.movies {
			if duplicate, ok := s.findDuplicateLocked(candidate.ID, candidate); ok {
				left, right = candidate, duplicate
				found = true
				break
			}
		}
		if !found {
			break
		}
		mergedMovie := mergeMoviesPreferNewer(left, right)
		delete(s.movies, left.ID)
		delete(s.movies, right.ID)
		s.movies[mergedMovie.ID] = mergedMovie
		merged++
	}
	if merged == 0 {
		return 0, nil
	}
	return merged, s.flushLocked()
}

func (s *Store) All(query string, fields []string) []Movie {
	s.mu.RLock()
	defer s.mu.RUnlock()
	query = strings.ToLower(strings.TrimSpace(query))
	fieldSet := searchFieldSet(fields)
	out := make([]Movie, 0, len(s.movies))
	for _, m := range s.movies {
		if query == "" && isOnlyMyRatingSearch(fieldSet) {
			if strings.TrimSpace(m.MyRating) == "" {
				out = append(out, m)
			}
			continue
		}
		if query == "" || movieMatches(m, query, fieldSet) {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out
}

func (s *Store) Get(id string) (Movie, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.movies[id]
	return m, ok
}

func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.movies)
}

func (s *Store) Save(m Movie) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if duplicate, ok := s.findDuplicateLocked(m.ID, m); ok {
		return fmt.Errorf("movie duplicates existing record %q", duplicate.Title)
	}
	now := time.Now()
	m = normalizeMovieForStorage(m, "", now)
	s.movies[m.ID] = m
	return s.flushLocked()
}

func (s *Store) AddResolvingDuplicate(m Movie, policy duplicatePolicy) (Movie, *Movie, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	m = normalizeMovieForStorage(m, "", now)
	if duplicate, ok := s.findDuplicateLocked("", m); ok {
		switch policy {
		case duplicateMergeNew:
			merged := mergeMoviesPreferNew(duplicate, m)
			s.movies[merged.ID] = merged
			if err := s.flushLocked(); err != nil {
				return Movie{}, nil, err
			}
			return merged, nil, nil
		case duplicateMergeOld:
			merged := mergeMoviesPreferOld(duplicate, m)
			s.movies[merged.ID] = merged
			if err := s.flushLocked(); err != nil {
				return Movie{}, nil, err
			}
			return merged, nil, nil
		case duplicateOverwrite:
			delete(s.movies, duplicate.ID)
			s.movies[m.ID] = m
			if err := s.flushLocked(); err != nil {
				return Movie{}, nil, err
			}
			return m, nil, nil
		case duplicateCancel, "":
			copy := duplicate
			return Movie{}, &copy, nil
		default:
			return Movie{}, nil, fmt.Errorf("unknown duplicate policy %q", policy)
		}
	}
	s.movies[m.ID] = m
	if err := s.flushLocked(); err != nil {
		return Movie{}, nil, err
	}
	return m, nil, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.movies, id)
	return s.flushLocked()
}

func (s *Store) findDuplicateLocked(skipID string, target Movie) (Movie, bool) {
	for _, existing := range s.movies {
		if existing.ID == skipID || target.ID != "" && existing.ID == target.ID {
			continue
		}
		if moviesAreDuplicates(existing, target) {
			return existing, true
		}
	}
	return Movie{}, false
}

func (s *Store) flushLocked() error {
	movies := make([]Movie, 0, len(s.movies))
	for _, m := range s.movies {
		movies = append(movies, m)
	}
	sort.Slice(movies, func(i, j int) bool {
		return movies[i].CreatedAt.Before(movies[j].CreatedAt)
	})
	tmp := s.path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(movies); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func movieMatches(m Movie, q string, fields map[string]bool) bool {
	if len(fields) == 0 {
		return false
	}
	for field := range fields {
		if field == "myRating" && isNumericSearch(q) {
			if strings.TrimSpace(m.MyRating) == q {
				return true
			}
			continue
		}
		if strings.Contains(strings.ToLower(movieFieldText(m, field)), q) {
			return true
		}
	}
	return false
}

func isNumericSearch(q string) bool {
	if q == "" {
		return false
	}
	for _, r := range q {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isOnlyMyRatingSearch(fields map[string]bool) bool {
	return len(fields) == 1 && fields["myRating"]
}

func searchFieldSet(fields []string) map[string]bool {
	out := map[string]bool{}
	if len(fields) == 0 {
		for _, field := range searchableFields() {
			out[field] = true
		}
		return out
	}
	valid := map[string]bool{}
	for _, field := range searchableFields() {
		valid[field] = true
	}
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if valid[field] {
			out[field] = true
		}
	}
	return out
}

func searchableFields() []string {
	return []string{
		"title",
		"format",
		"studio",
		"directors",
		"cast",
		"producers",
		"credits",
		"genre",
		"releaseDate",
		"runtime",
		"rating",
		"myRating",
		"synopsis",
		"sourceUrl",
		"amazonUrl",
		"location",
		"notes",
		"externalIds",
	}
}

func movieFieldText(m Movie, field string) string {
	switch field {
	case "title":
		return m.Title
	case "format":
		return m.Format
	case "studio":
		return m.Studio
	case "directors":
		return strings.Join(m.Directors, " ")
	case "cast":
		return strings.Join(m.Cast, " ")
	case "producers":
		return strings.Join(m.Producers, " ")
	case "credits":
		return mapText(m.Credits)
	case "genre":
		return strings.Join(m.Genre, " ")
	case "releaseDate":
		return m.ReleaseDate
	case "runtime":
		return m.Runtime
	case "rating":
		return m.Rating
	case "myRating":
		return m.MyRating
	case "synopsis":
		return m.Synopsis
	case "sourceUrl":
		return m.SourceURL
	case "amazonUrl":
		return m.AmazonURL
	case "location":
		return m.Location
	case "notes":
		return m.Notes
	case "externalIds":
		return mapText(m.ExternalIDs)
	default:
		return ""
	}
}

func mapText(values map[string]string) string {
	parts := make([]string, 0, len(values)*2)
	for key, value := range values {
		parts = append(parts, key, value)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func moviesAreDuplicates(a, b Movie) bool {
	if sharedExternalID(a, b) {
		return true
	}
	if asinA, asinB := amazonIdentity(a), amazonIdentity(b); asinA != "" && asinA == asinB {
		return true
	}
	titleA, titleB := normalizedMovieTitle(a.Title), normalizedMovieTitle(b.Title)
	if titleA == "" || titleA != titleB {
		return false
	}
	return sameOrMissingReleaseDate(a.ReleaseDate, b.ReleaseDate)
}

func sharedExternalID(a, b Movie) bool {
	for _, key := range []string{"tmdb", "imdb", "amazon_asin"} {
		left := strings.TrimSpace(a.ExternalIDs[key])
		right := strings.TrimSpace(b.ExternalIDs[key])
		if left != "" && right != "" && strings.EqualFold(left, right) {
			return true
		}
	}
	return false
}

func amazonIdentity(m Movie) string {
	if asin := strings.TrimSpace(m.ExternalIDs["amazon_asin"]); asin != "" {
		return strings.ToUpper(asin)
	}
	return strings.ToUpper(amazonASIN(m.AmazonURL))
}

func normalizedMovieTitle(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	title = yearInParensOrStandaloneRE.ReplaceAllString(title, "")
	title = nonAlnumLowerRE.ReplaceAllString(title, " ")
	title = strings.TrimSpace(title)
	return title
}

func sameOrMissingReleaseDate(a, b string) bool {
	dateA, dateB := normalizedReleaseDate(a), normalizedReleaseDate(b)
	if dateA == "" || dateB == "" {
		return true
	}
	if dateA == dateB {
		return true
	}
	yearA, yearB := releaseYear(dateA), releaseYear(dateB)
	return yearA != "" && yearA == yearB
}

func normalizedReleaseDate(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "n/a" {
		return ""
	}
	if t, err := time.Parse("2006-01-02", value); err == nil {
		return t.Format("2006-01-02")
	}
	for _, layout := range []string{"2 Jan 2006", "02 Jan 2006", "Jan 2 2006", "January 2 2006", "Jan 02 2006", "January 02 2006"} {
		if t, err := time.Parse(layout, strings.Title(value)); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if year := releaseYear(value); year != "" {
		return year
	}
	return value
}

func releaseYear(value string) string {
	match := releaseYearRE.FindString(value)
	return match
}

func normalizeMovieForStorage(m Movie, fallbackFormat string, now time.Time) Movie {
	if m.ID == "" {
		m.ID = newID()
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = now
	}
	if strings.TrimSpace(m.Format) == "" {
		m.Format = defaultString(fallbackFormat, "DVD")
	}
	if m.Credits == nil {
		m.Credits = map[string]string{}
	}
	if m.ExternalIDs == nil {
		m.ExternalIDs = map[string]string{}
	}
	m.UpdatedAt = now
	return m
}

func mergeMoviesPreferNew(old, new Movie) Movie {
	out := old
	out.Title = preferString(new.Title, old.Title)
	out.Format = preferString(new.Format, old.Format)
	out.Studio = preferString(new.Studio, old.Studio)
	out.Directors = preferSlice(new.Directors, old.Directors)
	out.Cast = preferSlice(new.Cast, old.Cast)
	out.Producers = preferSlice(new.Producers, old.Producers)
	out.Credits = mergeMapPreferNew(old.Credits, new.Credits)
	out.Genre = preferSlice(new.Genre, old.Genre)
	out.ReleaseDate = preferString(new.ReleaseDate, old.ReleaseDate)
	out.Runtime = preferString(new.Runtime, old.Runtime)
	out.Rating = preferString(new.Rating, old.Rating)
	out.MyRating = preferString(new.MyRating, old.MyRating)
	out.Synopsis = preferString(new.Synopsis, old.Synopsis)
	out.SourceURL = preferString(new.SourceURL, old.SourceURL)
	out.AmazonURL = preferString(new.AmazonURL, old.AmazonURL)
	out.ImagePath = preferString(new.ImagePath, old.ImagePath)
	out.Location = preferString(new.Location, old.Location)
	out.Notes = preferString(new.Notes, old.Notes)
	out.ExternalIDs = mergeMapPreferNew(old.ExternalIDs, new.ExternalIDs)
	out.UpdatedAt = time.Now()
	return out
}

func mergeMoviesPreferNewer(a, b Movie) Movie {
	if b.UpdatedAt.After(a.UpdatedAt) {
		return mergeMoviesPreferNew(a, b)
	}
	return mergeMoviesPreferOld(a, b)
}

func mergeMoviesPreferOld(old, new Movie) Movie {
	out := new
	out.ID = old.ID
	out.CreatedAt = old.CreatedAt
	out.Title = preferString(old.Title, new.Title)
	out.Format = preferString(old.Format, new.Format)
	out.Studio = preferString(old.Studio, new.Studio)
	out.Directors = preferSlice(old.Directors, new.Directors)
	out.Cast = preferSlice(old.Cast, new.Cast)
	out.Producers = preferSlice(old.Producers, new.Producers)
	out.Credits = mergeMapPreferNew(new.Credits, old.Credits)
	out.Genre = preferSlice(old.Genre, new.Genre)
	out.ReleaseDate = preferString(old.ReleaseDate, new.ReleaseDate)
	out.Runtime = preferString(old.Runtime, new.Runtime)
	out.Rating = preferString(old.Rating, new.Rating)
	out.MyRating = preferString(old.MyRating, new.MyRating)
	out.Synopsis = preferString(old.Synopsis, new.Synopsis)
	out.SourceURL = preferString(old.SourceURL, new.SourceURL)
	out.AmazonURL = preferString(old.AmazonURL, new.AmazonURL)
	out.ImagePath = preferString(old.ImagePath, new.ImagePath)
	out.Location = preferString(old.Location, new.Location)
	out.Notes = preferString(old.Notes, new.Notes)
	out.ExternalIDs = mergeMapPreferNew(new.ExternalIDs, old.ExternalIDs)
	out.UpdatedAt = time.Now()
	return out
}

type Server struct {
	store     *Store
	client    *http.Client
	imageDir  string
	imageBase string
}

// Application startup

func main() {
	dbPath := flag.String("db-path", "", "database directory containing movies.json and images/")
	port := flag.Int("port", appPort, "TCP port to listen on")
	var hosts hostList
	flag.Var(&hosts, "host", "host interface to listen on; may be repeated")
	flag.Parse()
	if !validPort(*port) {
		log.Fatalf("port must be between 1 and 65535, got %d", *port)
	}
	if len(hosts) == 0 {
		hosts = hostList{"127.0.0.1"}
	}

	root, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	dataDir := databaseDir(root, *dbPath)
	store, err := NewStore(filepath.Join(dataDir, "movies.json"))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("MovieDB database: %s (%d movie(s))\n", filepath.Join(dataDir, "movies.json"), store.Count())
	s := &Server{
		store:     store,
		client:    newOutboundClient(20 * time.Second),
		imageDir:  filepath.Join(dataDir, "images"),
		imageBase: "/images/",
	}
	if err := os.MkdirAll(s.imageDir, 0o755); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/lookup", s.handleLookup)
	mux.HandleFunc("/api/movies", s.handleMovies)
	mux.HandleFunc("/api/movies/", s.handleMovie)
	mux.HandleFunc("/images/", s.handleImageFile)
	webRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/moviedb/", http.StripPrefix("/moviedb/", http.FileServer(http.FS(webRoot))))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/moviedb/", http.StatusFound)
			return
		}
		http.NotFound(w, r)
	})

	listeners, err := listenOnHosts(hosts, *port)
	if err != nil {
		log.Fatal(err)
	}
	for _, ln := range listeners {
		fmt.Println("MovieDB is running at", browserURLForListener(ln))
	}
	go openBrowser(browserURLForListener(listeners[0]))
	errs := make(chan error, len(listeners))
	handler := securityMiddleware(newHostPolicy(hosts, *port), mux)
	for _, ln := range listeners {
		go func(listener net.Listener) {
			errs <- http.Serve(listener, handler)
		}(ln)
	}
	log.Fatal(<-errs)
}

func databaseDir(root, dbPath string) string {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return filepath.Join(root, "data")
	}
	return cleanAbsPath(dbPath)
}

func cleanAbsPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// Networking and security

func listenOnHosts(hosts []string, port int) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(hosts))
	for _, host := range hosts {
		addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, existing := range listeners {
				_ = existing.Close()
			}
			return nil, fmt.Errorf("listen on %s: %w", addr, err)
		}
		listeners = append(listeners, ln)
	}
	return listeners, nil
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func browserURLForListener(ln net.Listener) string {
	host, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		return "http://" + ln.Addr().String() + "/moviedb/"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/moviedb/"
}

type hostPolicy struct {
	port               string
	allowedHosts       map[string]bool
	allowPrivateIPHost bool
}

func newHostPolicy(hosts []string, port int) hostPolicy {
	policy := hostPolicy{
		port: strconv.Itoa(port),
		allowedHosts: map[string]bool{
			"127.0.0.1": true,
			"::1":       true,
			"localhost": true,
		},
	}
	for _, host := range hosts {
		host = canonicalHostname(host)
		if isWildcardHost(host) {
			policy.allowPrivateIPHost = true
			continue
		}
		if host != "" {
			policy.allowedHosts[host] = true
		}
	}
	return policy
}

func (p hostPolicy) allows(hostport string) bool {
	host, port := splitHostPortLenient(hostport)
	if host == "" {
		return false
	}
	if port != "" && p.port != "" && port != p.port {
		return false
	}
	if p.allowedHosts[host] {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && p.allowPrivateIPHost && isTrustedLANIP(ip)
}

func securityMiddleware(policy hostPolicy, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		if !policy.allows(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if isUnsafeMethod(r.Method) && !hasTrustedOrigin(r) {
			http.Error(w, "forbidden origin", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func hasTrustedOrigin(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return sameOriginHost(origin, r.Host)
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return sameOriginHost(referer, r.Host)
	}
	return true
}

func sameOriginHost(rawURL, requestHost string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return sameHostPort(u.Host, requestHost)
}

func sameHostPort(a, b string) bool {
	aHost, aPort := splitHostPortLenient(a)
	bHost, bPort := splitHostPortLenient(b)
	return aHost != "" && aHost == bHost && aPort == bPort
}

func splitHostPortLenient(value string) (string, string) {
	value = strings.TrimSpace(value)
	host, port, err := net.SplitHostPort(value)
	if err == nil {
		return canonicalHostname(host), port
	}
	return canonicalHostname(value), ""
}

func canonicalHostname(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimSuffix(value, ".")
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
	return value
}

func isWildcardHost(host string) bool {
	return host == "" || host == "0.0.0.0" || host == "::"
}

func isTrustedLANIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func newOutboundClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return safeDialContext(ctx, dialer, network, addr)
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return validateRemoteFetchURL(req.URL.String())
		},
	}
}

func safeDialContext(ctx context.Context, dialer *net.Dialer, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for %s", host)
	}
	for _, ip := range ips {
		if !isPublicInternetIP(ip.IP) {
			return nil, fmt.Errorf("refusing to connect to private address for %s", host)
		}
	}
	var firstErr error
	for _, ip := range ips {
		if network == "tcp4" && ip.IP.To4() == nil {
			continue
		}
		if network == "tcp6" && ip.IP.To4() != nil {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, fmt.Errorf("no usable addresses for %s", host)
}

func validateRemoteFetchURL(rawURL string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("remote URL must use http or https")
	}
	if u.User != nil {
		return errors.New("remote URL must not contain credentials")
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("remote URL host is required")
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicInternetIP(ip) {
		return fmt.Errorf("refusing to fetch private address %s", host)
	}
	return nil
}

func isPublicInternetIP(ip net.IP) bool {
	return ip != nil &&
		ip.IsGlobalUnicast() &&
		!ip.IsPrivate() &&
		!ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() &&
		!ip.IsUnspecified()
}

// HTTP handlers

func (s *Server) handleMovies(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, s.store.All(r.URL.Query().Get("q"), splitFields(r.URL.Query().Get("fields"))))
	case http.MethodPost:
		var req struct {
			Titles          []string `json:"titles"`
			Movie           *Movie   `json:"movie"`
			Format          string   `json:"format"`
			DuplicatePolicy string   `json:"duplicatePolicy"`
		}
		if !decodeJSONBody(w, r, &req) {
			return
		}
		if req.Movie != nil {
			m := *req.Movie
			if err := prepareMovieInput(&m, req.Format, s.imageBase); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if m.ID == "" {
				m.ID = newID()
			}
			saved, duplicate, err := s.store.AddResolvingDuplicate(m, duplicatePolicy(req.DuplicatePolicy))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if duplicate != nil {
				writeJSONStatus(w, http.StatusConflict, map[string]any{
					"error":     "duplicate movie",
					"candidate": m,
					"existing":  duplicate,
				})
				return
			}
			writeJSON(w, []Movie{saved})
			return
		}
		added := make([]Movie, 0, len(req.Titles))
		for _, entry := range normalizeTitles(req.Titles) {
			title := entry
			amazonURL := ""
			if isAmazonURL(entry) {
				title = entry
				amazonURL = entry
			}
			m := Movie{
				ID:          newID(),
				Title:       title,
				Format:      defaultString(req.Format, "DVD"),
				AmazonURL:   amazonURL,
				Credits:     map[string]string{},
				ExternalIDs: map[string]string{},
				CreatedAt:   time.Now(),
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			enriched, err := s.lookupMovie(ctx, m)
			cancel()
			if err != nil {
				enriched.Notes = strings.TrimSpace(enriched.Notes + "\nLookup note: " + err.Error())
			}
			saved, duplicate, err := s.store.AddResolvingDuplicate(enriched, duplicatePolicy(req.DuplicatePolicy))
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if duplicate != nil {
				writeJSONStatus(w, http.StatusConflict, map[string]any{
					"error":     "duplicate movie",
					"candidate": enriched,
					"existing":  duplicate,
				})
				return
			}
			added = append(added, saved)
		}
		writeJSON(w, added)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]int{"totalMovies": s.store.Count()})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dest any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dest); err != nil {
		writeRequestDecodeError(w, err)
		return false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			http.Error(w, "request body must contain only one JSON value", http.StatusBadRequest)
		} else {
			writeRequestDecodeError(w, err)
		}
		return false
	}
	return true
}

func writeRequestDecodeError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func prepareMovieInput(m *Movie, fallbackFormat, imageBase string) error {
	m.Title = strings.TrimSpace(m.Title)
	if m.Title == "" {
		return errors.New("title is required")
	}
	m.Format = strings.TrimSpace(m.Format)
	if m.Format == "" {
		m.Format = defaultString(fallbackFormat, "DVD")
	}
	if m.Credits == nil {
		m.Credits = map[string]string{}
	}
	if m.ExternalIDs == nil {
		m.ExternalIDs = map[string]string{}
	}
	m.ImagePath = strings.TrimSpace(m.ImagePath)
	if m.ImagePath != "" && !isSafeLocalImagePath(m.ImagePath, imageBase) {
		return errors.New("image path must reference a local JPEG, PNG, GIF, or WebP image")
	}
	return nil
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Title  string `json:"title"`
		Format string `json:"format"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	candidates, err := s.lookupCandidates(ctx, title, defaultString(req.Format, "DVD"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, candidates)
}

func (s *Server) handleMovie(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/movies/"), "/"), "/")
	id := ""
	if len(parts) > 0 {
		id = parts[0]
	}
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && parts[1] == "image" {
		s.handleMovieImage(w, r, id)
		return
	}
	if len(parts) == 2 && parts[1] == "refresh" {
		s.handleMovieRefresh(w, r, id)
		return
	}
	if len(parts) > 1 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		m, ok := s.store.Get(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, m)
	case http.MethodPut:
		var m Movie
		if !decodeJSONBody(w, r, &m) {
			return
		}
		if err := prepareMovieInput(&m, "", s.imageBase); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m.ID = id
		var old Movie
		var hadOld bool
		old, hadOld = s.store.Get(id)
		if hadOld {
			m.CreatedAt = old.CreatedAt
		}
		if err := s.store.Save(m); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if hadOld && old.ImagePath != "" && m.ImagePath != "" && old.ImagePath != m.ImagePath && !s.imagePathUsedByAnotherMovie(id, old.ImagePath) {
			if err := s.deleteMovieImageFile(old.ImagePath); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		writeJSON(w, m)
	case http.MethodDelete:
		if err := s.store.Delete(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMovieRefresh(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	m, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	refreshed, err := s.refreshMovieFromSource(ctx, m)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, refreshed)
}

func (s *Server) handleMovieImage(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodPost:
		s.handleMovieImageUpload(w, r, id)
	case http.MethodDelete:
		s.handleMovieImageDelete(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMovieImageUpload(w http.ResponseWriter, r *http.Request, id string) {
	m, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMultipartBodyBytes)
	if err := r.ParseMultipartForm(25 << 20); err != nil {
		writeRequestDecodeError(w, err)
		return
	}
	file, header, err := r.FormFile("cover")
	if err != nil {
		http.Error(w, "cover image is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	imagePath, err := s.saveUploadedImage(&m, file, header.Filename)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.ImagePath = imagePath
	if err := s.store.Save(m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, m)
}

func (s *Server) handleMovieImageDelete(w http.ResponseWriter, r *http.Request, id string) {
	m, ok := s.store.Get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	oldPath := m.ImagePath
	m.ImagePath = ""
	if err := s.store.Save(m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if oldPath != "" && !s.imagePathUsedByAnotherMovie(id, oldPath) {
		if err := s.deleteMovieImageFile(oldPath); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	writeJSON(w, m)
}

func (s *Server) handleImageFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, s.imageBase)
	if !safeImageFileName(name) {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.imageDir, name))
}

func (s *Server) imagePathUsedByAnotherMovie(movieID, imagePath string) bool {
	for _, movie := range s.store.All("", nil) {
		if movie.ID != movieID && movie.ImagePath == imagePath {
			return true
		}
	}
	return false
}

// Lookup orchestration

func (s *Server) lookupCandidates(ctx context.Context, title, format string) ([]LookupCandidate, error) {
	if isAmazonURL(title) {
		m := newCandidateBase(title, format)
		m.AmazonURL = title
		if asin := amazonASIN(title); asin != "" {
			m.ExternalIDs["amazon_asin"] = asin
		}
		return []LookupCandidate{{
			Movie:       m,
			MatchType:   "exact",
			Provider:    "Amazon",
			Description: "Stored Amazon product URL without scraping",
		}}, nil
	}

	var approximate []LookupCandidate
	var errs []string
	if tmdbKey, tmdbToken := strings.TrimSpace(os.Getenv("TMDB_API_KEY")), strings.TrimSpace(os.Getenv("TMDB_BEARER_TOKEN")); tmdbKey != "" || tmdbToken != "" {
		exact, approx, err := s.lookupTMDbCandidates(ctx, title, format, tmdbKey, tmdbToken)
		if err == nil {
			if len(exact) > 0 {
				return exact, nil
			}
			approximate = append(approximate, approx...)
		} else {
			errs = append(errs, "TMDb: "+err.Error())
		}
	}
	if key := strings.TrimSpace(os.Getenv("OMDB_API_KEY")); key != "" {
		m := newCandidateBase(title, format)
		if out, err := s.lookupOMDb(ctx, m, key); err == nil && normalizedMovieTitle(out.Title) == normalizedMovieTitle(title) {
			return []LookupCandidate{{
				Movie:       out,
				MatchType:   "exact",
				Provider:    "OMDb",
				Description: candidateDescription(out),
			}}, nil
		} else if err != nil {
			errs = append(errs, "OMDb: "+err.Error())
		}
	}
	if len(approximate) == 0 {
		if exact, approx, err := s.lookupWikidataCandidates(ctx, title, format); err == nil {
			if len(exact) > 0 {
				return exact, nil
			}
			approximate = append(approximate, approx...)
		} else {
			errs = append(errs, "Wikidata: "+err.Error())
		}
	}
	if len(approximate) > 0 {
		return dedupeCandidates(approximate), nil
	}
	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "; "))
	}
	return nil, errors.New("no movie matches found")
}

func newCandidateBase(title, format string) Movie {
	return Movie{
		ID:          newID(),
		Title:       title,
		Format:      defaultString(format, "DVD"),
		Credits:     map[string]string{},
		ExternalIDs: map[string]string{},
		CreatedAt:   time.Now(),
	}
}

func candidateDescription(m Movie) string {
	parts := []string{m.ReleaseDate, strings.Join(m.Genre, ", "), m.Studio}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, " - ")
}

func (s *Server) lookupMovie(ctx context.Context, m Movie) (Movie, error) {
	var errs []string
	if m.AmazonURL != "" {
		if m.ExternalIDs == nil {
			m.ExternalIDs = map[string]string{}
		}
		if asin := amazonASIN(m.AmazonURL); asin != "" {
			m.ExternalIDs["amazon_asin"] = asin
		}
	}
	if tmdbKey, tmdbToken := strings.TrimSpace(os.Getenv("TMDB_API_KEY")), strings.TrimSpace(os.Getenv("TMDB_BEARER_TOKEN")); tmdbKey != "" || tmdbToken != "" {
		if out, err := s.lookupTMDb(ctx, m, tmdbKey, tmdbToken); err == nil {
			return out, nil
		} else {
			errs = append(errs, "TMDb: "+err.Error())
		}
	}
	if key := strings.TrimSpace(os.Getenv("OMDB_API_KEY")); key != "" {
		if out, err := s.lookupOMDb(ctx, m, key); err == nil {
			return out, nil
		} else {
			errs = append(errs, "OMDb: "+err.Error())
		}
	}
	if out, err := s.lookupWikidata(ctx, m); err == nil {
		return out, nil
	} else {
		errs = append(errs, "Wikidata: "+err.Error())
	}
	return m, errors.New(strings.Join(errs, "; "))
}

func (s *Server) refreshMovieFromSource(ctx context.Context, old Movie) (Movie, error) {
	refreshed := old
	var errs []string
	tmdbKey, tmdbToken := strings.TrimSpace(os.Getenv("TMDB_API_KEY")), strings.TrimSpace(os.Getenv("TMDB_BEARER_TOKEN"))
	if tmdbID := strings.TrimSpace(old.ExternalIDs["tmdb"]); tmdbID != "" && (tmdbKey != "" || tmdbToken != "") {
		if id, err := strconv.Atoi(tmdbID); err == nil {
			if out, err := s.enrichTMDbMovie(ctx, old, id, tmdbKey, tmdbToken); err == nil {
				return preserveLocalMovieFields(old, out), nil
			} else {
				errs = append(errs, "TMDb: "+err.Error())
			}
		}
	}
	if key := strings.TrimSpace(os.Getenv("OMDB_API_KEY")); key != "" {
		if imdb := strings.TrimSpace(old.ExternalIDs["imdb"]); imdb != "" {
			if out, err := s.lookupOMDbByID(ctx, old, key, imdb); err == nil {
				return preserveLocalMovieFields(old, out), nil
			} else {
				errs = append(errs, "OMDb: "+err.Error())
			}
		}
	}
	if out, err := s.lookupMovie(ctx, refreshed); err == nil {
		return preserveLocalMovieFields(old, out), nil
	} else {
		errs = append(errs, err.Error())
	}
	return old, errors.New(strings.Join(errs, "; "))
}

func preserveLocalMovieFields(old, refreshed Movie) Movie {
	refreshed.ID = old.ID
	refreshed.CreatedAt = old.CreatedAt
	refreshed.UpdatedAt = time.Now()
	refreshed.Format = old.Format
	refreshed.Location = old.Location
	refreshed.Notes = old.Notes
	refreshed.MyRating = old.MyRating
	refreshed.AmazonURL = preferString(old.AmazonURL, refreshed.AmazonURL)
	if refreshed.ImagePath == "" {
		refreshed.ImagePath = old.ImagePath
	}
	return refreshed
}

// TMDb provider

func (s *Server) lookupTMDb(ctx context.Context, m Movie, apiKey, bearerToken string) (Movie, error) {
	results, err := s.searchTMDb(ctx, m.Title, apiKey, bearerToken)
	if err != nil {
		return m, err
	}
	if len(results) == 0 {
		return m, errors.New("movie not found")
	}
	return s.enrichTMDbMovie(ctx, m, results[0].ID, apiKey, bearerToken)
}

type tmdbSearchResult struct {
	ID          int    `json:"id"`
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
	Overview    string `json:"overview"`
}

func (s *Server) searchTMDb(ctx context.Context, title, apiKey, bearerToken string) ([]tmdbSearchResult, error) {
	searchURL := "https://api.themoviedb.org/3/search/movie?include_adult=false&language=en-US&page=1&query=" + url.QueryEscape(title)
	if apiKey != "" {
		searchURL += "&api_key=" + url.QueryEscape(apiKey)
	}
	var search struct {
		Results []tmdbSearchResult `json:"results"`
	}
	if err := getTMDbJSON(ctx, s.client, searchURL, bearerToken, &search); err != nil {
		return nil, err
	}
	return search.Results, nil
}

func (s *Server) enrichTMDbMovie(ctx context.Context, m Movie, tmdbID int, apiKey, bearerToken string) (Movie, error) {
	detailsURL := fmt.Sprintf("https://api.themoviedb.org/3/movie/%d?append_to_response=credits,external_ids,release_dates&language=en-US", tmdbID)
	if apiKey != "" {
		detailsURL += "&api_key=" + url.QueryEscape(apiKey)
	}
	var details struct {
		ID                  int     `json:"id"`
		Title               string  `json:"title"`
		Overview            string  `json:"overview"`
		ReleaseDate         string  `json:"release_date"`
		Runtime             int     `json:"runtime"`
		PosterPath          string  `json:"poster_path"`
		Genres              []named `json:"genres"`
		ProductionCompanies []named `json:"production_companies"`
		Credits             struct {
			Cast []castMember `json:"cast"`
			Crew []crewMember `json:"crew"`
		} `json:"credits"`
		ExternalIDs struct {
			IMDbID string `json:"imdb_id"`
		} `json:"external_ids"`
		ReleaseDates struct {
			Results []tmdbReleaseCountry `json:"results"`
		} `json:"release_dates"`
	}
	if err := getTMDbJSON(ctx, s.client, detailsURL, bearerToken, &details); err != nil {
		return m, err
	}
	m.Title = defaultString(details.Title, m.Title)
	m.Synopsis = defaultString(details.Overview, m.Synopsis)
	m.ReleaseDate = defaultString(details.ReleaseDate, m.ReleaseDate)
	if details.Runtime > 0 {
		m.Runtime = fmt.Sprintf("%d min", details.Runtime)
	}
	m.Rating = preferString(tmdbMPARating(details.ReleaseDates.Results), m.Rating)
	m.Genre = names(details.Genres)
	if studios := names(details.ProductionCompanies); len(studios) > 0 {
		m.Studio = strings.Join(studios, ", ")
	}
	m.Directors = crewByJob(details.Credits.Crew, "Director")
	m.Producers = crewByJob(details.Credits.Crew, "Producer")
	m.Cast = firstN(castNames(details.Credits.Cast), 12)
	if m.Credits == nil {
		m.Credits = map[string]string{}
	}
	for _, job := range []string{"Screenplay", "Writer", "Original Music Composer", "Director of Photography"} {
		if people := crewByJob(details.Credits.Crew, job); len(people) > 0 {
			m.Credits[job] = strings.Join(people, ", ")
		}
	}
	if m.ExternalIDs == nil {
		m.ExternalIDs = map[string]string{}
	}
	m.ExternalIDs["tmdb"] = fmt.Sprintf("%d", details.ID)
	if details.ExternalIDs.IMDbID != "" {
		m.ExternalIDs["imdb"] = details.ExternalIDs.IMDbID
	}
	m.SourceURL = fmt.Sprintf("https://www.themoviedb.org/movie/%d", details.ID)
	if details.PosterPath != "" {
		_ = s.cacheImage(ctx, &m, "https://image.tmdb.org/t/p/w500"+details.PosterPath)
	}
	return m, nil
}

type tmdbReleaseCountry struct {
	ISO31661     string `json:"iso_3166_1"`
	ReleaseDates []struct {
		Certification string `json:"certification"`
		Type          int    `json:"type"`
	} `json:"release_dates"`
}

func tmdbMPARating(countries []tmdbReleaseCountry) string {
	for _, country := range countries {
		if country.ISO31661 != "US" {
			continue
		}
		for _, preferredType := range []int{3, 2, 1, 4, 5, 6} {
			for _, release := range country.ReleaseDates {
				certification := strings.TrimSpace(release.Certification)
				if certification != "" && release.Type == preferredType {
					return certification
				}
			}
		}
		for _, release := range country.ReleaseDates {
			if certification := strings.TrimSpace(release.Certification); certification != "" {
				return certification
			}
		}
	}
	return ""
}

func (s *Server) lookupTMDbCandidates(ctx context.Context, title, format, apiKey, bearerToken string) ([]LookupCandidate, []LookupCandidate, error) {
	results, err := s.searchTMDb(ctx, title, apiKey, bearerToken)
	if err != nil {
		return nil, nil, err
	}
	if len(results) == 0 {
		results, err = s.searchTMDbWidened(ctx, title, apiKey, bearerToken)
		if err != nil {
			return nil, nil, err
		}
	}
	var exact []LookupCandidate
	var approximate []LookupCandidate
	for i, result := range results {
		if i >= 8 {
			break
		}
		m := newCandidateBase(result.Title, format)
		enriched, err := s.enrichTMDbMovie(ctx, m, result.ID, apiKey, bearerToken)
		if err != nil {
			continue
		}
		matchType := "approximate"
		if normalizedMovieTitle(enriched.Title) == normalizedMovieTitle(title) {
			matchType = "exact"
		}
		candidate := LookupCandidate{
			Movie:       enriched,
			MatchType:   matchType,
			Provider:    "TMDb",
			Description: candidateDescription(enriched),
		}
		if matchType == "exact" {
			exact = append(exact, candidate)
		} else {
			approximate = append(approximate, candidate)
		}
	}
	return dedupeCandidates(exact), dedupeCandidates(approximate), nil
}

func (s *Server) searchTMDbWidened(ctx context.Context, title, apiKey, bearerToken string) ([]tmdbSearchResult, error) {
	for _, query := range widenedTitleQueries(title) {
		results, err := s.searchTMDb(ctx, query, apiKey, bearerToken)
		if err != nil {
			return nil, err
		}
		if len(results) > 0 {
			return results, nil
		}
	}
	return nil, errors.New("movie not found")
}

// OMDb provider

func (s *Server) lookupOMDb(ctx context.Context, m Movie, key string) (Movie, error) {
	u := "https://www.omdbapi.com/?apikey=" + url.QueryEscape(key) + "&plot=full&r=json&t=" + url.QueryEscape(m.Title)
	return s.lookupOMDbURL(ctx, m, u)
}

func (s *Server) lookupOMDbByID(ctx context.Context, m Movie, key, imdbID string) (Movie, error) {
	u := "https://www.omdbapi.com/?apikey=" + url.QueryEscape(key) + "&plot=full&r=json&i=" + url.QueryEscape(imdbID)
	return s.lookupOMDbURL(ctx, m, u)
}

func (s *Server) lookupOMDbURL(ctx context.Context, m Movie, u string) (Movie, error) {
	var res struct {
		Response   string
		Error      string
		Title      string
		Year       string
		Rated      string
		Released   string
		Runtime    string
		Genre      string
		Director   string
		Writer     string
		Actors     string
		Plot       string
		Poster     string
		Production string
		IMDbID     string `json:"imdbID"`
	}
	if err := getJSON(ctx, s.client, u, &res); err != nil {
		return m, err
	}
	if strings.EqualFold(res.Response, "false") {
		return m, errors.New(defaultString(res.Error, "movie not found"))
	}
	m.Title = defaultString(res.Title, m.Title)
	m.Studio = res.Production
	m.Directors = splitCSV(res.Director)
	m.Cast = splitCSV(res.Actors)
	m.Genre = splitCSV(res.Genre)
	m.ReleaseDate = defaultString(res.Released, res.Year)
	m.Runtime = res.Runtime
	m.Rating = res.Rated
	m.Synopsis = res.Plot
	m.SourceURL = "https://www.imdb.com/title/" + res.IMDbID + "/"
	if m.ExternalIDs == nil {
		m.ExternalIDs = map[string]string{}
	}
	m.ExternalIDs["imdb"] = res.IMDbID
	if res.Writer != "" {
		m.Credits["Writer"] = res.Writer
	}
	if res.Poster != "" && res.Poster != "N/A" {
		_ = s.cacheImage(ctx, &m, res.Poster)
	}
	return m, nil
}

// Wikidata and Wikipedia providers

func (s *Server) lookupWikidata(ctx context.Context, m Movie) (Movie, error) {
	searchURL := "https://www.wikidata.org/w/api.php?action=wbsearchentities&language=en&format=json&type=item&limit=5&search=" + url.QueryEscape(m.Title+" film")
	var search struct {
		Search []struct {
			ID          string `json:"id"`
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"search"`
	}
	if err := getJSON(ctx, s.client, searchURL, &search); err != nil {
		return m, err
	}
	if len(search.Search) == 0 {
		return m, errors.New("movie not found")
	}
	entityID := search.Search[0].ID
	entityURL := "https://www.wikidata.org/wiki/Special:EntityData/" + url.PathEscape(entityID) + ".json"
	var data wikidataResponse
	if err := getJSON(ctx, s.client, entityURL, &data); err != nil {
		return m, err
	}
	entity, ok := data.Entities[entityID]
	if !ok {
		return m, errors.New("entity data missing")
	}
	labels := collectReferencedIDs(entity)
	labelMap := s.wikidataLabels(ctx, labels)
	m.Title = entity.label("en", m.Title)
	m.Directors = labelsForClaims(entity, "P57", labelMap)
	m.Cast = firstN(labelsForClaims(entity, "P161", labelMap), 12)
	m.Producers = labelsForClaims(entity, "P162", labelMap)
	m.Genre = labelsForClaims(entity, "P136", labelMap)
	studios := labelsForClaims(entity, "P272", labelMap)
	if len(studios) > 0 {
		m.Studio = strings.Join(studios, ", ")
	}
	if release := firstTimeClaim(entity, "P577"); release != "" {
		m.ReleaseDate = release
	}
	if imdb := firstStringClaim(entity, "P345"); imdb != "" {
		m.ExternalIDs["imdb"] = imdb
		m.SourceURL = "https://www.imdb.com/title/" + imdb + "/"
	}
	if m.SourceURL == "" {
		m.SourceURL = "https://www.wikidata.org/wiki/" + entityID
	}
	if title, ok := entity.Sitelinks["enwiki"]; ok {
		s.fillWikipediaSummary(ctx, &m, title.Title)
	}
	if image := firstStringClaim(entity, "P18"); image != "" && m.ImagePath == "" {
		commons := "https://commons.wikimedia.org/wiki/Special:Redirect/file/" + url.PathEscape(image)
		_ = s.cacheImage(ctx, &m, commons)
	}
	return m, nil
}

func (s *Server) lookupWikidataCandidates(ctx context.Context, title, format string) ([]LookupCandidate, []LookupCandidate, error) {
	results, err := s.searchWikidata(ctx, title+" film")
	if err != nil {
		return nil, nil, err
	}
	if len(results) == 0 {
		for _, query := range widenedTitleQueries(title) {
			results, err = s.searchWikidata(ctx, query+" film")
			if err != nil {
				return nil, nil, err
			}
			if len(results) > 0 {
				break
			}
		}
	}
	if len(results) == 0 {
		return nil, nil, errors.New("movie not found")
	}
	var exact []LookupCandidate
	var approximate []LookupCandidate
	for i, result := range results {
		if i >= 8 {
			break
		}
		m := newCandidateBase(result.Label, format)
		m.SourceURL = "https://www.wikidata.org/wiki/" + result.ID
		m.Notes = strings.TrimSpace(result.Description)
		matchType := "approximate"
		if normalizedMovieTitle(result.Label) == normalizedMovieTitle(title) {
			matchType = "exact"
		}
		candidate := LookupCandidate{
			Movie:       m,
			MatchType:   matchType,
			Provider:    "Wikidata",
			Description: result.Description,
		}
		if matchType == "exact" {
			exact = append(exact, candidate)
		} else {
			approximate = append(approximate, candidate)
		}
	}
	return dedupeCandidates(exact), dedupeCandidates(approximate), nil
}

type wikidataSearchResult struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

func (s *Server) searchWikidata(ctx context.Context, query string) ([]wikidataSearchResult, error) {
	searchURL := "https://www.wikidata.org/w/api.php?action=wbsearchentities&language=en&format=json&type=item&limit=10&search=" + url.QueryEscape(query)
	var search struct {
		Search []wikidataSearchResult `json:"search"`
	}
	if err := getJSON(ctx, s.client, searchURL, &search); err != nil {
		return nil, err
	}
	return search.Search, nil
}

func (s *Server) wikidataLabels(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	for start := 0; start < len(ids); start += 50 {
		end := start + 50
		if end > len(ids) {
			end = len(ids)
		}
		u := "https://www.wikidata.org/w/api.php?action=wbgetentities&props=labels&languages=en&format=json&ids=" + url.QueryEscape(strings.Join(ids[start:end], "|"))
		var res wikidataResponse
		if err := getJSON(ctx, s.client, u, &res); err != nil {
			continue
		}
		for id, e := range res.Entities {
			out[id] = e.label("en", id)
		}
	}
	return out
}

func (s *Server) fillWikipediaSummary(ctx context.Context, m *Movie, title string) {
	u := "https://en.wikipedia.org/api/rest_v1/page/summary/" + url.PathEscape(title)
	var res struct {
		Extract     string `json:"extract"`
		ContentURLs struct {
			Desktop struct {
				Page string `json:"page"`
			} `json:"desktop"`
		} `json:"content_urls"`
		Thumbnail struct {
			Source string `json:"source"`
		} `json:"thumbnail"`
		OriginalImage struct {
			Source string `json:"source"`
		} `json:"originalimage"`
	}
	if err := getJSON(ctx, s.client, u, &res); err != nil {
		return
	}
	if m.Synopsis == "" {
		m.Synopsis = res.Extract
	}
	if res.ContentURLs.Desktop.Page != "" && !strings.Contains(m.SourceURL, "imdb.com") {
		m.SourceURL = res.ContentURLs.Desktop.Page
	}
	img := defaultString(res.OriginalImage.Source, res.Thumbnail.Source)
	if img != "" {
		_ = s.cacheImage(ctx, m, img)
	}
}

// Remote fetching and image storage

func (s *Server) fetchHTML(ctx context.Context, rawURL string) (string, string, error) {
	if err := validateRemoteFetchURL(rawURL); err != nil {
		return "", rawURL, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", rawURL, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) MovieDB/1.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", rawURL, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", rawURL, fmt.Errorf("request failed: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if err != nil {
		return "", rawURL, err
	}
	return string(data), resp.Request.URL.String(), nil
}

func (s *Server) cacheImage(ctx context.Context, m *Movie, imageURL string) error {
	if err := validateRemoteFetchURL(imageURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "MovieDB/1.0")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("image download failed: %s", resp.Status)
	}
	data, ext, err := readValidatedImage(resp.Body)
	if err != nil {
		return err
	}
	name := safeFilePart(m.ID) + ext
	path := filepath.Join(s.imageDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	m.ImagePath = s.imageBase + name
	return nil
}

func (s *Server) saveUploadedImage(m *Movie, file io.Reader, _ string) (string, error) {
	data, ext, err := readValidatedImage(file)
	if err != nil {
		return "", err
	}
	name := safeFilePart(m.ID) + "-cover-" + time.Now().Format("20060102150405") + ext
	path := filepath.Join(s.imageDir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return s.imageBase + name, nil
}

func readValidatedImage(r io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxImageBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) == 0 {
		return nil, "", errors.New("image is empty")
	}
	if len(data) > maxImageBytes {
		return nil, "", errors.New("image is too large")
	}
	contentType := http.DetectContentType(data)
	ext, ok := allowedImageContentTypes[contentType]
	if !ok {
		return nil, "", errors.New("image must be JPEG, PNG, GIF, or WebP")
	}
	return data, ext, nil
}

func isSafeLocalImagePath(imagePath, imageBase string) bool {
	imagePath = strings.TrimSpace(imagePath)
	if imagePath == "" {
		return true
	}
	if !strings.HasPrefix(imagePath, imageBase) {
		return false
	}
	return safeImageFileName(strings.TrimPrefix(imagePath, imageBase))
}

func safeImageFileName(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !allowedImageExtensions[ext] {
		return false
	}
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if stem == "" || strings.HasPrefix(stem, ".") {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return true
}

func (s *Server) deleteMovieImageFile(imagePath string) error {
	if strings.TrimSpace(imagePath) == "" {
		return nil
	}
	if !strings.HasPrefix(imagePath, s.imageBase) {
		return nil
	}
	name := strings.TrimPrefix(imagePath, s.imageBase)
	if !safeImageFileName(name) {
		return fmt.Errorf("refusing to delete unsafe image path %q", imagePath)
	}
	target := filepath.Join(s.imageDir, name)
	cleanDir, err := filepath.Abs(s.imageDir)
	if err != nil {
		return err
	}
	cleanTarget, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Dir(cleanTarget) != cleanDir {
		return fmt.Errorf("refusing to delete image outside image directory")
	}
	if err := os.Remove(cleanTarget); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Provider data helpers

type named struct {
	Name string `json:"name"`
}

type crewMember struct {
	Name string `json:"name"`
	Job  string `json:"job"`
}

type castMember struct {
	Name string `json:"name"`
}

func names(values []named) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Name)
	}
	return unique(out)
}

func crewByJob(values []crewMember, job string) []string {
	var out []string
	for _, v := range values {
		if strings.EqualFold(v.Job, job) {
			out = append(out, v.Name)
		}
	}
	return unique(out)
}

func castNames(values []castMember) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v.Name)
	}
	return unique(out)
}

type wikidataResponse struct {
	Entities map[string]wikidataEntity `json:"entities"`
}

type wikidataEntity struct {
	Labels map[string]struct {
		Value string `json:"value"`
	} `json:"labels"`
	Claims map[string][]struct {
		Mainsnak struct {
			Datavalue struct {
				Value any `json:"value"`
			} `json:"datavalue"`
		} `json:"mainsnak"`
	} `json:"claims"`
	Sitelinks map[string]struct {
		Title string `json:"title"`
	} `json:"sitelinks"`
}

func (e wikidataEntity) label(lang, fallback string) string {
	if v, ok := e.Labels[lang]; ok && v.Value != "" {
		return v.Value
	}
	return fallback
}

func collectReferencedIDs(e wikidataEntity) []string {
	seen := map[string]bool{}
	for _, prop := range []string{"P57", "P161", "P162", "P272", "P136"} {
		for _, claim := range e.Claims[prop] {
			if id := entityIDFromClaimValue(claim.Mainsnak.Datavalue.Value); id != "" {
				seen[id] = true
			}
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func labelsForClaims(e wikidataEntity, prop string, labels map[string]string) []string {
	var out []string
	for _, claim := range e.Claims[prop] {
		id := entityIDFromClaimValue(claim.Mainsnak.Datavalue.Value)
		if id != "" {
			out = append(out, defaultString(labels[id], id))
		}
	}
	return unique(out)
}

func entityIDFromClaimValue(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if id, ok := m["id"].(string); ok {
		return id
	}
	if numeric, ok := m["numeric-id"].(float64); ok {
		return fmt.Sprintf("Q%.0f", numeric)
	}
	return ""
}

func firstStringClaim(e wikidataEntity, prop string) string {
	for _, claim := range e.Claims[prop] {
		if s, ok := claim.Mainsnak.Datavalue.Value.(string); ok {
			return s
		}
	}
	return ""
}

func firstTimeClaim(e wikidataEntity, prop string) string {
	for _, claim := range e.Claims[prop] {
		m, ok := claim.Mainsnak.Datavalue.Value.(map[string]any)
		if !ok {
			continue
		}
		t, ok := m["time"].(string)
		if !ok {
			continue
		}
		return strings.TrimPrefix(strings.SplitN(t, "T", 2)[0], "+")
	}
	return ""
}

// Shared lookup and formatting helpers

func getTMDbJSON(ctx context.Context, client *http.Client, u, bearerToken string, dest any) error {
	headers := map[string]string{}
	if bearerToken != "" {
		headers["Authorization"] = "Bearer " + bearerToken
	}
	return getJSONWithHeaders(ctx, client, u, headers, dest)
}

func getJSON(ctx context.Context, client *http.Client, u string, dest any) error {
	return getJSONWithHeaders(ctx, client, u, nil, dest)
}

func getJSONWithHeaders(ctx context.Context, client *http.Client, u string, headers map[string]string, dest any) error {
	if err := validateRemoteFetchURL(u); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "MovieDB/1.0")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("request failed: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxJSONBodyBytes)).Decode(dest)
}

func normalizeTitles(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' || r == ';' }) {
			title := strings.TrimSpace(part)
			if title != "" && !seen[strings.ToLower(title)] {
				seen[strings.ToLower(title)] = true
				out = append(out, title)
			}
		}
	}
	return out
}

func splitFields(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	fields := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			fields = append(fields, part)
		}
	}
	return fields
}

func isAmazonURL(value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Host)
	return host == "amazon.com" || strings.HasSuffix(host, ".amazon.com")
}

func amazonASIN(value string) string {
	if match := asinPathRE.FindStringSubmatch(value); len(match) == 2 {
		return match[1]
	}
	return ""
}

func widenedTitleQueries(title string) []string {
	seen := map[string]bool{}
	add := func(value string, out *[]string) {
		value = strings.TrimSpace(value)
		value = whitespaceRE.ReplaceAllString(value, " ")
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			seen[key] = true
			*out = append(*out, value)
		}
	}
	var out []string
	add(title, &out)
	add(parentheticalRE.ReplaceAllString(title, ""), &out)
	add(yearInParensOrStandaloneRE.ReplaceAllString(title, ""), &out)
	words := strings.Fields(nonAlnumTitleRE.ReplaceAllString(title, " "))
	for size := len(words) - 1; size >= 1; size-- {
		add(strings.Join(words[:size], " "), &out)
	}
	return out
}

func dedupeCandidates(candidates []LookupCandidate) []LookupCandidate {
	seen := map[string]bool{}
	out := make([]LookupCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		key := candidateKey(candidate.Movie)
		if key == "" {
			key = normalizedMovieTitle(candidate.Movie.Title) + "|" + candidate.Movie.ReleaseDate
		}
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, candidate)
		}
	}
	return out
}

func candidateKey(m Movie) string {
	for _, key := range []string{"tmdb", "imdb", "amazon_asin"} {
		if value := strings.TrimSpace(m.ExternalIDs[key]); value != "" {
			return key + ":" + strings.ToLower(value)
		}
	}
	if asin := amazonASIN(m.AmazonURL); asin != "" {
		return "amazon_asin:" + strings.ToLower(asin)
	}
	return ""
}

func splitCSV(s string) []string {
	if s == "" || s == "N/A" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && part != "N/A" {
			out = append(out, part)
		}
	}
	return out
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		key := strings.ToLower(strings.TrimSpace(v))
		if key != "" && !seen[key] {
			seen[key] = true
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func preferString(primary, fallback string) string {
	if strings.TrimSpace(primary) != "" {
		return primary
	}
	return fallback
}

func preferSlice(primary, fallback []string) []string {
	if len(primary) > 0 {
		return unique(primary)
	}
	return unique(fallback)
}

func mergeMapPreferNew(old, new map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range old {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	for key, value := range new {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[:n]
}

func writeJSON(w http.ResponseWriter, value any) {
	writeJSONStatus(w, http.StatusOK, value)
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func safeFilePart(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return newID()
	}
	return b.String()
}

func openBrowser(addr string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", addr)
	case "darwin":
		cmd = exec.Command("open", addr)
	default:
		cmd = exec.Command("xdg-open", addr)
	}
	_ = cmd.Start()
}
