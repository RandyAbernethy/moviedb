# MovieDB

A local movie collection database written in Go.

The executable runs a local web server and presents a browser based UI via embedded html/css/js. Movie data is stored
locally in a movies.json file under `data/` with cover art saved in `data/images/`.


## Build

You can build and run the application on Windows in PowerShell with:

```powershell
go run .
```

The app opens automatically in your default browser.

To build a Windows executable use:

```powershell
go build -o moviedb.exe .
```

To run the executable, just run it!

```powershell
./moviedb.exe
```

This is a Go program with a browser based UI so it will need very few (if any) tweaks to run on Linux or Mac, I just
haven't gotten around to it.


## Use

The browser UI has three panes:

- Search Pane - used to add and search for movies
- Movie List - shows movies matching the current search criteria in the Search Pane
- Movie Detail View - shows detailed information about the movie selected in the Movie List


## Internet data

MovieDB can load movie cover art and data from the internet, however most internet sites actively repel automated
scraping attempts. To allow MovieDB to pull down movie information reliably, you will have best luck if you create an
account at one of the online movie database sites and then generate an API key for MovieDB to use.

The Movie Database (TMDB) is a popular, user editable database for movies and TV shows and perhaps the best option for
MovieDB. MovieDB can enrich titles with TMDB data if the `TMDB_API_KEY` or `TMDB_BEARER_TOKEN` environment variable is
set. When looking up movie data, MovieDB checks TMDB first if a key or token is set.

If TMDB settings are not found, MovieDB will check for an Internet Movie Database (IMDB) key in the `OMDB_API_KEY`
environment variable.

If no keys are set MovieDB attempts to load data from public Wikidata and Wikipedia data. This almost always fails these
days due to anti-scrape and rate limiting.

The final new movie screen will be populated with data from one of the available sources, depending on which API keys
are set and the site willingness to respond. You can always add desired data manually.

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


### Amazon

You can paste Amazon product URLs into the add box as well, one per line, and MovieDB will make a best-effort scrape of
the public product page for the title, cover image, description, and ASIN. Amazon scraping is intentionally best-effort
because Amazon often blocks or changes automated page access.

Plain title imports do not search Amazon by default. To opt in to Amazon search scraping for plain titles:

```powershell
$env:MOVIEDB_AMAZON_SEARCH="1"
./moviedb.exe
```


## Duplicates

MovieDB does not allow duplicate movie records for the same movie, but it does allow different movies with the same
title when their release dates differ. On startup it scans the local database and merges duplicates automatically,
randomly choosing between conflicting populated fields. During import, duplicate matches produce a dialog that lets you
cancel, merge new data into the existing record, merge old data into the new record, or overwrite the old record.
