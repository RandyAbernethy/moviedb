# MovieDB

A movie collection database application written in Go.

The executable runs a local web server (e.g. http://127.0.0.1:8765/moviedb/) and presents a browser based UI via
embedded html/css/js. Movie data is stored locally in a movies.json file under `./data/` with cover art saved in
discrete files under `./data/images/`.

MovieDB exists to provide a simple, local solution for DVD/Blu-ray movie collection management without dependencies on
external services or the internet. Features:

- Makes it easy to add movies manually or automatically in large batches using online databases (*the one connected feature*)
- Stores all of the information about movies locally with no need for internet access
- Free and open source (requires no subscriptions)
- Single, small, fast, 0-run-time-dependency Go binary: download it, run it
- Minimal dev dependencies, generic Go and Javascript and the Go standard library (no other imports/requires)
- Uses a single simple json file for all movie data (except cover art images), copy and edit as you like
- Uses an `images/` directory to house all cover art files in standard viewable graphics formats (jfif, png, jpeg, etc.)
- Allows you to store location information (which binder, shelf or cabinet the movie is in)
- Allows you to rate your movies
- Allows you to add notes to movies
- Everything is searchable, making it easy to find movies with a given actor or from a specific genre or with a "watch next" note or ...
- Works on desktops, laptops, good on tablets and decent on phones
- A single moviedb instance can be accessed from multiple machines, tablets and phones on your home network or the internet (consider security implications)

The app is about 10MB and a 500 movie database is about 1MB of JSON plus 200MB of cover art (and cover art is optional).
The repo contains a sample database in the `./data/` directory.


## Build/Run

To build a Windows executable:

```powershell
go build -o moviedb.exe .
```

To run the executable, just run it!

```powershell
./moviedb.exe
```

The app should open in your default browser.

To build a Linux or Mac executable:

```bash
go build -o moviedb .
```

The app supports a few command line switches:

```powershell
PS C:\moviedb> .\moviedb.exe -h

Usage of C:\moviedb\moviedb.exe:
  -db-path string
        database directory containing movies.json and images/
  -host value
        host interface to listen on; may be repeated
  -port int
        TCP port to listen on (default 8765)

PS C:\moviedb> 
```

By default, MovieDB listens on localhost port `8765`. You can ask the app to listen on all IPv4 interfaces and/or a different port:

```powershell
./moviedb.exe --host 0.0.0.0 --port 8080
```

To listen on specific interfaces, use as many host switches as you require `--host`:

```powershell
./moviedb.exe --host 127.0.0.1 --host 192.168.1.25
```

By default, MovieDB looks for the database under `./data/` relative to the run directory. To choose an alternate
database directory, use `--db-path`. The directory should contain `movies.json`; cover art is expected in an `images/`
subdirectory inside that same directory. If `movies.json` does not exist in the selected directory, MovieDB creates an
empty database there:

```powershell
./moviedb.exe --db-path "D:\MovieDB\data"
```

This is a Go program with a browser based UI so it will need very few (if any) tweaks to run on Linux or Mac, I just
haven't gotten around to testing it. The app has been tested on Windows with Chrome.


## Use

The browser UI has three drag-sizable panes:

- **Add/Search** - automatically add and search for movies
    - You can drop a list of movie titles in the Add box to add in bulk, a dialog provides deconfliction and merge options as needed
    - You can also search for movies by title, genre, year, actor, etc. or multiple fields to find what you are looking for quickly
- **Movie List** - displays a list of movies matching the current search criteria in the Search Pane
    - Sorted by the field you choose
    - Increasing or decreasing order as you choose
    - Optionally ignore leading "the" in titles
    - Navigate up/down with arrow keys or press an alphanumeric key to jump to movies starting with that character
- **Movie Details** - shows detailed information about the movie currently selected in the Movie List
    - Allows you to create new movies manually by clicking the "New" button at the bottom of the pane
    - Allows you to edit any field including cover art (changes are lost when you navigate away unless you press "Save changes")
    - You can drag/drop or copy/paste cover art to update it or use the COVER ART CHANGE/DELETE buttons
    - To pull fresh data from the internet for a movie, click the "Update from source" button at the bottom of the details pane
    - To delete a movie altogether, click the "Delete" button at the bottom

> N.B. Edits made in the UI are only saved if you click "Save changes"!! This includes creating new movies with "New",
> editing movies, updating Cover Art and updating from the internet with "Update from source". All changes are lost when
> you navigate away from a movie unless you save. Adding movies automatically in the left pane persists movie additions
> immediately after deconfliction dialogs are answered with a savable option.


---

![UI](./moviedb-screenshot-2026-06-20-164644.png)

---


## Internet data

MovieDB pulls movie cover art and data from the internet into your local database when you use the "Add movie titles"
box in the Add/Search pane. However, most internet sites actively repel automated scraping attempts, so to allow MovieDB
to pull down movie information reliably, you should create an account at one of the online movie database sites and then
and then generate an API key for MovieDB to use.

"The Movie Database" (TMDB) is a popular, user editable database for movies and TV shows and perhaps the best option for
MovieDB. MovieDB can enrich titles with TMDB data if the `TMDB_API_KEY` or `TMDB_BEARER_TOKEN` environment variable is
set. When looking up movie data, MovieDB checks TMDB first if a TMDB key or token is set.

If TMDB settings are not found, MovieDB will check for an Internet Movie Database (IMDB) key in the `OMDB_API_KEY`
environment variable. If found it will use the OMDB API to retrieve data from IMDB.

If no keys are set, MovieDB attempts to load data from public Wikidata and Wikipedia data. This almost always fails
these days due to anti-scrape and rate limiting but you can always enter your own data and cover art manually.

To set keys in PowerShell:

```powershell
$env:TMDB_API_KEY="your_tmdb_v3_api_key"
./moviedb.exe
```

or:

```powershell
$env:OMDB_API_KEY="your_omdb_api_key"
./moviedb.exe
```


## Duplicates

MovieDB does not allow duplicate movies, a movie is unique if the combination of its title and release date are unique.
On startup the app scans the local database and merges duplicates title/date movies automatically (duplicates should not
occur during normal operations but if they do this startup check will repair your DB so that you can continue to use the
app). During add operations, duplicate movies produce a dialog that lets you:

- Cancel - aborts the add with no data changed
- Merge new data - Copies new data into the existing record leaving old data intact when new fields are blank
- Merge old data - Copies old data into the new record leaving new data intact when old fields are blank
- Overwrite - Deletes the old record and adds the new record in its place

If you have concerns about your database, make a backup of the ./data/movies.json file. You can always restore a backup
by shutting down the application and then just copying over the old movies.json file with your backup. Also, because
movies.json is just a json file, you can edit it manually with any decent editor (e.g. notepad++, vscode, vim, etc.).
If you want to preserve your cover art, you should make a copy of the images directory too.
