import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";

class FakeEvent {
  constructor(type, options = {}) {
    this.type = type;
    this.key = options.key || "";
    this.altKey = Boolean(options.altKey);
    this.ctrlKey = Boolean(options.ctrlKey);
    this.metaKey = Boolean(options.metaKey);
    this.dataTransfer = options.dataTransfer || null;
    this.clipboardData = options.clipboardData || null;
    this.target = options.target || null;
    this.submitter = options.submitter || null;
    this.defaultPrevented = false;
    this.propagationStopped = false;
  }

  preventDefault() {
    this.defaultPrevented = true;
  }

  stopPropagation() {
    this.propagationStopped = true;
  }
}

class FakeElement {
  constructor(tagName, ownerDocument) {
    this.tagName = tagName.toUpperCase();
    this.ownerDocument = ownerDocument;
    this.children = [];
    this.eventListeners = {};
    this.dataset = {};
    this.className = "";
    this.value = "";
    this.textContent = "";
    this.style = {};
    this.id = "";
  }

  click() {
    this.ownerDocument.clickedElements.push(this);
    this.dispatchEvent(new FakeEvent("click"));
  }

  remove() {
    if (!this.parentElement) return;
    this.parentElement.children = this.parentElement.children.filter((child) => child !== this);
    this.parentElement = null;
  }

  appendChild(child) {
    child.parentElement = this;
    this.children.push(child);
    return child;
  }

  append(...children) {
    for (const child of children) {
      if (typeof child === "string") continue;
      this.appendChild(child);
    }
  }

  replaceChildren(...children) {
    this.children = [];
    this.append(...children);
  }

  addEventListener(type, callback) {
    this.eventListeners[type] ||= [];
    this.eventListeners[type].push(callback);
  }

  dispatchEvent(event) {
    event.currentTarget = this;
    event.target ||= this;
    for (const callback of this.eventListeners[event.type] || []) {
      callback(event);
    }
    return !event.defaultPrevented;
  }

  focus() {
    this.ownerDocument.activeElement = this;
  }

  scrollIntoView() {}

  setPointerCapture() {}

  requestSubmit(submitter = null) {
    this.dispatchEvent(new FakeEvent("submit", { submitter }));
  }

  classList = {
    add: (...names) => {
      const existing = new Set(this.className.split(/\s+/).filter(Boolean));
      for (const name of names) existing.add(name);
      this.className = [...existing].join(" ");
    },
    remove: (...names) => {
      const remove = new Set(names);
      this.className = this.className.split(/\s+/).filter((name) => !remove.has(name)).join(" ");
    },
    contains: (name) => this.className.split(/\s+/).includes(name),
    toggle: (name, force) => {
      const has = this.classList.contains(name);
      const shouldAdd = force === undefined ? !has : Boolean(force);
      if (shouldAdd) {
        this.classList.add(name);
      } else {
        this.classList.remove(name);
      }
      return shouldAdd;
    },
  };

  querySelector(selector) {
    return findFirst(this, selector);
  }

  contains(element) {
    if (this === element) return true;
    return this.children.some((child) => child.contains(element));
  }
}

class FakeDocument {
  constructor() {
    this.elementsById = new Map();
    this.body = this.createElement("body");
    this.activeElement = null;
    this.clickedElements = [];
  }

  createElement(tagName) {
    return new FakeElement(tagName, this);
  }

  getElementById(id) {
    if (!this.elementsById.has(id)) {
      const element = this.createElement(id === "movieForm" ? "form" : "div");
      element.id = id;
      this.elementsById.set(id, element);
    }
    return this.elementsById.get(id);
  }

  querySelector(selector) {
    return findFirst(this.body, selector) || findFirstInMap(this.elementsById, selector);
  }

  querySelectorAll(selector) {
    const out = [];
    for (const element of this.elementsById.values()) collectMatches(element, selector, out);
    collectMatches(this.body, selector, out);
    return [...new Set(out)];
  }

  addEventListener(type, callback) {
    this.body.addEventListener(type, callback);
  }
}

function matches(element, selector) {
  if (selector === "#fieldList input:checked") {
    return element.tagName === "INPUT" && element.checked;
  }
  if (selector === "#fieldList input") {
    return element.tagName === "INPUT";
  }
  if (selector === ".resizer") {
    return element.classList.contains("resizer");
  }
  if (selector === ".result.active") {
    return element.classList.contains("result") && element.classList.contains("active");
  }
  if (selector === ".result") {
    return element.classList.contains("result");
  }
  if (/^\.[A-Za-z0-9_-]+$/.test(selector)) {
    return element.classList.contains(selector.slice(1));
  }
  const movieIDMatch = selector.match(/^\.result\[data-movie-id="([^"]+)"\]$/);
  if (movieIDMatch) {
    return element.classList.contains("result") && element.dataset.movieId === movieIDMatch[1];
  }
  if (selector === "input") {
    return element.tagName === "INPUT";
  }
  return false;
}

function collectMatches(element, selector, out) {
  if (matches(element, selector)) out.push(element);
  for (const child of element.children) collectMatches(child, selector, out);
}

function findFirst(element, selector) {
  if (matches(element, selector)) return element;
  for (const child of element.children) {
    const found = findFirst(child, selector);
    if (found) return found;
  }
  return null;
}

function findFirstInMap(map, selector) {
  for (const element of map.values()) {
    const found = findFirst(element, selector);
    if (found) return found;
  }
  return null;
}

const document = new FakeDocument();
const requiredIDs = [
  "title", "movieFormat", "studio", "directors", "cast", "producers", "genre",
  "releaseDate", "runtime", "rating", "myRating", "synopsis", "sourceUrl", "amazonUrl",
  "location", "notes", "search", "fieldList", "resultCount", "results",
  "sortField", "sortDirection", "empty", "movieForm", "poster", "addForm",
  "titles", "status", "selectAllFields", "clearFields", "deleteButton",
  "totalMovies", "coverArt", "coverStatus", "posterTarget", "deleteCoverArt", "refreshButton",
  "newButton", "emptyNewButton", "ignoreLeadingThe", "ignoreLeadingTheLabel",
  "ignoreLeadingA", "ignoreLeadingALabel", "downloadList",
];
for (const id of requiredIDs) document.getElementById(id);
const app = document.createElement("main");
app.className = "app";
document.body.appendChild(app);
document.body.appendChild(document.getElementById("results"));

const movies = [
  { id: "n", title: "2001: A Space Odyssey", format: "Blu-ray", genre: [], releaseDate: "1968" },
  {
    id: "a",
    title: "Alpha",
    format: "DVD",
    studio: "Studio, One",
    directors: ["Director One", "Director Two"],
    cast: ["Actor One"],
    producers: ["Producer One"],
    credits: { Writer: "Writer One" },
    genre: [],
    releaseDate: "2001",
    runtime: "101 min",
    rating: "PG",
    myRating: "8",
    synopsis: "A quoted \"summary\"",
    sourceUrl: "https://example.com/source",
    amazonUrl: "https://example.com/amazon",
    imagePath: "/images/alpha-cover.jpg",
    location: "Shelf A",
    notes: "Has, comma",
    externalIds: { imdb: "tt-alpha" },
    createdAt: "2020-01-01T00:00:00Z",
    updatedAt: "2020-01-02T00:00:00Z",
  },
  { id: "bug", title: "A Bug's Life", format: "DVD", genre: [], releaseDate: "1998" },
  { id: "alps", title: "Alps", format: "DVD", genre: [], releaseDate: "2011" },
  { id: "abyss", title: "The Abyss", format: "DVD", genre: [], releaseDate: "1989" },
  { id: "t", title: "The Artist", format: "DVD", genre: [], releaseDate: "2011" },
  { id: "b", title: "Bravo", format: "DVD", genre: [], releaseDate: "2002" },
  { id: "c", title: "Charlie", format: "DVD", genre: [], releaseDate: "2003" },
  { id: "matrix", title: "The Matrix", format: "DVD", genre: [], releaseDate: "1999" },
  { id: "term", title: "Terminator", format: "DVD", genre: [], releaseDate: "1984" },
];
const requests = [];
const objectURLs = [];
const downloadBlobs = [];
const revokedObjectURLs = [];
class FakeBlob {
  constructor(parts, options = {}) {
    this.parts = parts;
    this.type = options.type || "";
  }
}
const objectURLAPI = {
  createObjectURL(file) {
    const isNamedFile = file && file.name;
    const url = isNamedFile ? `blob:${file.name}` : `blob:download-${downloadBlobs.length + 1}`;
    if (!isNamedFile) {
      downloadBlobs.push(file);
    }
    objectURLs.push(url);
    return url;
  },
  revokeObjectURL(url) {
    revokedObjectURLs.push(url);
  },
};

function jsonResponse(payload) {
  return {
    ok: true,
    status: 200,
    text: async () => JSON.stringify(payload),
    json: async () => payload,
  };
}

function textResponse(status, text) {
  return {
    ok: status >= 200 && status < 300,
    status,
    text: async () => text,
    json: async () => JSON.parse(text),
  };
}

const context = {
  document,
  window: {
    innerWidth: 1200,
    addEventListener() {},
    searchTimer: null,
    URL: objectURLAPI,
  },
  localStorage: {
    getItem() { return null; },
    setItem() {},
  },
  fetch: async (path, options = {}) => {
    const requestPath = String(path);
    requests.push({ path: requestPath, method: options.method || "GET", options });
    if (requestPath.startsWith("/api/stats")) {
      return jsonResponse({ totalMovies: movies.length });
    }
    if (requestPath.startsWith("/api/lookup")) {
      const payload = JSON.parse(options.body || "{}");
      if (payload.title === "Missing Movie") {
        return textResponse(404, "no movie matches found");
      }
      return jsonResponse([{
        matchType: "exact",
        movie: {
          id: "lookup-id",
          title: payload.title || "Source Movie",
          format: "Blu-ray",
          genre: ["Drama"],
          releaseDate: "1999",
          imagePath: "/images/source.jpg",
        },
      }]);
    }
    if (requestPath === "/api/movies" && options.method === "POST") {
      const payload = JSON.parse(options.body);
      return jsonResponse([{ ...payload.movie, id: "draft-cover-id", imagePath: "" }]);
    }
    if (requestPath === "/api/movies/a" && options.method === "DELETE") {
      return textResponse(204, "");
    }
    if (requestPath.startsWith("/api/movies/") && options.method === "PUT") {
      return jsonResponse(JSON.parse(options.body));
    }
    if (requestPath === "/api/movies/a/refresh" && options.method === "POST") {
      return jsonResponse({
        id: "a",
        title: "Source Movie",
        format: "DVD",
        genre: ["Drama"],
        releaseDate: "1999",
        imagePath: "/images/source.jpg",
      });
    }
    if (requestPath === "/api/movies/draft-cover-id/image" && options.method === "POST") {
      return jsonResponse({
        id: "draft-cover-id",
        title: "Draft Cover",
        format: "DVD",
        genre: [],
        releaseDate: "",
        imagePath: "/images/draft-cover.jpg",
      });
    }
    return jsonResponse(movies);
  },
  clearTimeout,
  setTimeout,
  Date,
  Number,
  URL: objectURLAPI,
  URLSearchParams,
  Blob: FakeBlob,
  FormData: class FormData {
    append() {}
  },
  CSS: { escape: (value) => String(value).replaceAll('"', '\\"') },
  console,
  alert: () => {},
  confirm: () => true,
  prompt: () => "",
};

vm.createContext(context);
vm.runInContext(fs.readFileSync("web/app.js", "utf8"), context);
await new Promise((resolve) => setTimeout(resolve, 0));

const results = document.getElementById("results");
const sortedMovieIDs = () => Array.from(context.sortedMovies(), (movie) => movie.id);
assert.equal(document.getElementById("title").value, "", "detail view starts empty");

document.getElementById("downloadList").dispatchEvent(new FakeEvent("click"));
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(downloadBlobs.length, 1, "download list creates a CSV blob");
assert.ok(document.clickedElements.at(-1).download.startsWith("moviedb-list-"), "download list names the CSV file");
const downloadedCSV = downloadBlobs.at(-1).parts.join("");
const csvLines = downloadedCSV.split(/\r?\n/).filter(Boolean);
assert.equal(csvLines[0], "ID,Title,Format,Studio,Directors,Cast,Producers,Credits,Genre,Release Date,Runtime,MPA Rating,MyRating,Synopsis,Source URL,Amazon URL,Cover Art,Location,Notes,External IDs,Created,Updated", "download list exports every movie field");
const alphaCSV = csvLines.find((line) => line.includes(",Alpha,"));
assert.ok(alphaCSV.includes("alpha-cover.jpg"), "download list exports cover art filename");
assert.ok(!alphaCSV.includes("/images/alpha-cover.jpg"), "download list omits cover art path");
assert.ok(alphaCSV.includes("Director One; Director Two"), "download list serializes list fields");
assert.ok(alphaCSV.includes("imdb: tt-alpha"), "download list serializes non-display map fields");

document.getElementById("titles").value = "Missing Movie\nFound Movie";
document.getElementById("addForm").dispatchEvent(new FakeEvent("submit", { submitter: document.createElement("button") }));
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(requests.some((request) => request.path === "/api/lookup" && request.options.body.includes("Missing Movie")), "bulk add tries the missing first title");
assert.ok(requests.some((request) => request.path === "/api/lookup" && request.options.body.includes("Found Movie")), "bulk add continues after a missing title");
assert.ok(requests.some((request) => request.path === "/api/lookup" && request.options.body.includes('"format":"DVD"')), "bulk add lookups default to DVD");
assert.ok(requests.some((request) => request.path === "/api/movies" && request.method === "POST" && request.options.body.includes('"format":"DVD"')), "bulk add saves default format as DVD");
assert.equal(document.getElementById("status").textContent, "Added 1 movie; skipped 1: Missing Movie.", "bulk add reports skipped missing titles");
assert.equal(document.getElementById("titles").value, "", "bulk add clears title list after processing misses and successes");

document.getElementById("emptyNewButton").dispatchEvent(new FakeEvent("click"));
assert.equal(document.getElementById("movieFormat").value, "DVD", "new blank movies default to DVD");
document.getElementById("title").value = "Source Movie";
document.getElementById("title").dispatchEvent(new FakeEvent("input"));
assert.equal(document.getElementById("refreshButton").disabled, false, "draft with title can update from source");
document.getElementById("refreshButton").dispatchEvent(new FakeEvent("click"));
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(document.getElementById("title").value, "Source Movie", "draft source update populates title");
assert.equal(document.getElementById("releaseDate").value, "1999", "draft source update populates release date");
assert.equal(document.getElementById("deleteButton").disabled, true, "source-updated draft is still unsaved");

document.getElementById("emptyNewButton").dispatchEvent(new FakeEvent("click"));
assert.equal(document.getElementById("coverArt").disabled, false, "draft cover file input is enabled before save");
assert.equal(document.getElementById("posterTarget").classList.contains("disabled"), false, "draft poster drop zone is enabled before save");
const draftCover = { name: "draft-cover.png", type: "image/png", size: 4 };
const draftDrop = new FakeEvent("drop", { dataTransfer: { files: [draftCover] } });
document.getElementById("posterTarget").dispatchEvent(draftDrop);
assert.equal(draftDrop.defaultPrevented, true, "draft cover drop prevents the browser default");
assert.equal(document.getElementById("poster").src, "blob:draft-cover.png", "draft cover drop previews the staged image");
assert.equal(document.getElementById("poster").hidden, false, "draft cover preview is visible before save");
assert.equal(document.getElementById("deleteCoverArt").disabled, false, "staged draft cover can be removed before save");
assert.equal(document.getElementById("coverStatus").textContent, "Cover art ready. Save changes to upload.", "draft cover is staged before save");
document.getElementById("title").value = "Draft Cover";
document.getElementById("movieForm").dispatchEvent(new FakeEvent("submit"));
await new Promise((resolve) => setTimeout(resolve, 0));
assert.ok(requests.some((request) => request.path === "/api/movies/draft-cover-id/image" && request.method === "POST"), "staged draft cover uploads after the movie is created");
assert.equal(document.getElementById("poster").src, "/images/draft-cover.jpg", "saved draft shows the uploaded cover path");
assert.ok(revokedObjectURLs.includes("blob:draft-cover.png"), "temporary draft cover preview URL is revoked after upload");

context.openMovie("a", { focusResult: true });
assert.equal(document.getElementById("title").value, "Alpha", "opening first movie populates detail view");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "a", "first movie is active");

document.getElementById("notes").value = "Saved by shortcut";
const ctrlSave = new FakeEvent("keydown", { key: "s", ctrlKey: true });
document.body.dispatchEvent(ctrlSave);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(ctrlSave.defaultPrevented, true, "Ctrl+S prevents the browser save-page shortcut");
assert.ok(requests.some((request) => request.path === "/api/movies/a" && request.method === "PUT" && request.options.body.includes("Saved by shortcut")), "Ctrl+S saves the open movie detail form");
assert.equal(document.getElementById("status").textContent, "Saved Alpha.", "Ctrl+S reports a saved movie");

document.getElementById("notes").value = "Saved by command shortcut";
const commandSave = new FakeEvent("keydown", { key: "S", metaKey: true });
document.body.dispatchEvent(commandSave);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(commandSave.defaultPrevented, true, "Cmd+S prevents the browser save-page shortcut");
assert.ok(requests.some((request) => request.path === "/api/movies/a" && request.method === "PUT" && request.options.body.includes("Saved by command shortcut")), "Cmd+S saves the open movie detail form");

const ctrlUpdate = new FakeEvent("keydown", { key: "u", ctrlKey: true });
document.body.dispatchEvent(ctrlUpdate);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(ctrlUpdate.defaultPrevented, true, "Ctrl+U prevents browser default while updating from source");
assert.ok(requests.some((request) => request.path === "/api/movies/a/refresh" && request.method === "POST"), "Ctrl+U updates the open movie from source");
assert.equal(document.getElementById("status").textContent, "Loaded source updates for Source Movie. Click \"Save changes\" to write them to the database.", "Ctrl+U reports source update status");

context.openMovie("a", { focusResult: true });
const commandUpdate = new FakeEvent("keydown", { key: "U", metaKey: true });
document.body.dispatchEvent(commandUpdate);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(commandUpdate.defaultPrevented, true, "Cmd+U prevents browser default while updating from source");
assert.ok(requests.filter((request) => request.path === "/api/movies/a/refresh" && request.method === "POST").length >= 2, "Cmd+U updates the open movie from source");

const ctrlNew = new FakeEvent("keydown", { key: "n", ctrlKey: true });
document.body.dispatchEvent(ctrlNew);
assert.equal(ctrlNew.defaultPrevented, false, "Ctrl+N is left to the browser");
assert.equal(document.getElementById("title").value, "Source Movie", "Ctrl+N no longer opens a new blank movie");

const commandNew = new FakeEvent("keydown", { key: "N", metaKey: true });
document.body.dispatchEvent(commandNew);
assert.equal(commandNew.defaultPrevented, false, "Cmd+N is left to the browser");
assert.equal(document.getElementById("title").value, "Source Movie", "Cmd+N no longer opens a new blank movie");

const insertNew = new FakeEvent("keydown", { key: "Insert" });
document.body.dispatchEvent(insertNew);
assert.equal(insertNew.defaultPrevented, true, "Insert prevents the browser default while opening a new movie");
assert.equal(document.getElementById("title").value, "", "Insert opens a new blank movie");
assert.equal(document.getElementById("movieFormat").value, "DVD", "Insert new movie defaults to DVD");

context.openMovie("a", { focusResult: true });
const deleteTextTarget = document.createElement("input");
deleteTextTarget.type = "text";
const deleteRequestsBeforeText = requests.filter((request) => request.path === "/api/movies/a" && request.method === "DELETE").length;
const textDelete = new FakeEvent("keydown", { key: "Delete", target: deleteTextTarget });
document.body.dispatchEvent(textDelete);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(textDelete.defaultPrevented, false, "Delete is left alone while editing text");
assert.equal(requests.filter((request) => request.path === "/api/movies/a" && request.method === "DELETE").length, deleteRequestsBeforeText, "Delete in a text field does not delete the open movie");
assert.equal(document.getElementById("title").value, "Alpha", "Delete in a text field leaves the open movie unchanged");

const deleteShortcut = new FakeEvent("keydown", { key: "Delete" });
document.body.dispatchEvent(deleteShortcut);
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(deleteShortcut.defaultPrevented, true, "Delete prevents the browser default while deleting the open movie");
assert.ok(requests.some((request) => request.path === "/api/movies/a" && request.method === "DELETE"), "Delete deletes the open movie");
assert.equal(document.getElementById("movieForm").classList.contains("hidden"), true, "Delete hides the detail form after deleting");
assert.equal(document.getElementById("empty").classList.contains("hidden"), false, "Delete returns to the empty detail state");

context.openMovie("a", { focusResult: true });
const down = new FakeEvent("keydown", { key: "ArrowDown" });
results.dispatchEvent(down);
assert.equal(down.defaultPrevented, true, "ArrowDown is handled by the results list");
assert.equal(document.getElementById("title").value, "Alps", "ArrowDown opens next movie in detail view");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "alps", "ArrowDown moves active tile");

const up = new FakeEvent("keydown", { key: "ArrowUp" });
results.dispatchEvent(up);
assert.equal(up.defaultPrevented, true, "ArrowUp is handled by the results list");
assert.equal(document.getElementById("title").value, "Alpha", "ArrowUp opens previous movie in detail view");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "a", "ArrowUp moves active tile");

const firstAJump = new FakeEvent("keydown", { key: "a" });
results.dispatchEvent(firstAJump);
assert.equal(firstAJump.defaultPrevented, true, "letter keys are handled by the results list");
assert.equal(document.getElementById("title").value, "Alps", "repeated letter jump advances to the next matching title");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "alps", "letter jump moves active tile to next match");

const secondAJump = new FakeEvent("keydown", { key: "a" });
results.dispatchEvent(secondAJump);
assert.equal(document.getElementById("title").value, "A Bug's Life", "letter jump wraps to the first matching title");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "bug", "letter jump wraps active tile");

const noMatchJump = new FakeEvent("keydown", { key: "z" });
results.dispatchEvent(noMatchJump);
assert.equal(noMatchJump.defaultPrevented, true, "unmatched letter keys are still handled by the results list");
assert.equal(document.getElementById("title").value, "A Bug's Life", "unmatched letter jump leaves the current movie unchanged");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "bug", "unmatched letter jump leaves active tile unchanged");

const letterJump = new FakeEvent("keydown", { key: "c" });
results.dispatchEvent(letterJump);
assert.equal(letterJump.defaultPrevented, true, "letter keys are handled by the results list");
assert.equal(document.getElementById("title").value, "Charlie", "letter key jumps to first matching title");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "c", "letter key moves active tile");

const numberJump = new FakeEvent("keydown", { key: "2" });
results.dispatchEvent(numberJump);
assert.equal(numberJump.defaultPrevented, true, "number keys are handled by the results list");
assert.equal(document.getElementById("title").value, "2001: A Space Odyssey", "number key jumps to first matching title");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "n", "number key moves active tile");

const shortcut = new FakeEvent("keydown", { key: "f", ctrlKey: true });
results.dispatchEvent(shortcut);
assert.equal(shortcut.defaultPrevented, false, "modified letter shortcuts are not intercepted");
assert.equal(document.getElementById("title").value, "2001: A Space Odyssey", "modified shortcut does not change selection");

document.getElementById("ignoreLeadingThe").checked = true;
document.getElementById("ignoreLeadingThe").dispatchEvent(new FakeEvent("change"));
assert.deepEqual(sortedMovieIDs(), ["n", "bug", "abyss", "a", "alps", "t", "b", "c", "matrix", "term"], "title sort can ignore leading The");

const ignoredArticleJump = new FakeEvent("keydown", { key: "t" });
results.dispatchEvent(ignoredArticleJump);
assert.equal(ignoredArticleJump.defaultPrevented, true, "letter jump still handles keys while ignoring leading The");
assert.equal(document.getElementById("title").value, "Terminator", "letter jump skips leading-The titles when ignore-leading-the is active");

const ignoredArticleAJump = new FakeEvent("keydown", { key: "a" });
results.dispatchEvent(ignoredArticleAJump);
assert.equal(document.getElementById("title").value, "A Bug's Life", "leading-A titles remain under A when only leading The is ignored");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "bug", "letter jump wraps to leading-A title");

document.getElementById("ignoreLeadingThe").focus();
const ignoredArticleGlobalJump = new FakeEvent("keydown", { key: "m" });
document.body.dispatchEvent(ignoredArticleGlobalJump);
assert.equal(ignoredArticleGlobalJump.defaultPrevented, true, "global letter jump handles keys after the ignore-leading-the checkbox has focus");
assert.equal(document.getElementById("title").value, "The Matrix", "global letter jump respects ignore-leading-the sorting");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "matrix", "global letter jump moves active tile to stripped-prefix match");

document.getElementById("ignoreLeadingThe").checked = false;
document.getElementById("ignoreLeadingThe").dispatchEvent(new FakeEvent("change"));
document.getElementById("ignoreLeadingA").checked = true;
document.getElementById("ignoreLeadingA").dispatchEvent(new FakeEvent("change"));
assert.deepEqual(sortedMovieIDs(), ["n", "a", "alps", "b", "bug", "c", "term", "abyss", "t", "matrix"], "title sort can ignore leading A");

document.getElementById("ignoreLeadingThe").checked = true;
document.getElementById("ignoreLeadingThe").dispatchEvent(new FakeEvent("change"));
assert.deepEqual(sortedMovieIDs(), ["n", "abyss", "a", "alps", "t", "b", "bug", "c", "matrix", "term"], "title sort can ignore both leading articles");

document.getElementById("sortField").value = "releaseDate";
document.getElementById("sortField").dispatchEvent(new FakeEvent("change"));
assert.equal(document.getElementById("ignoreLeadingThe").disabled, true, "ignore-leading-the is disabled for non-title sorts");
assert.equal(document.getElementById("ignoreLeadingA").disabled, true, "ignore-leading-a is disabled for non-title sorts");
assert.equal(document.getElementById("ignoreLeadingThe").checked, true, "ignore-leading-the preference is preserved while disabled");
assert.equal(document.getElementById("ignoreLeadingA").checked, true, "ignore-leading-a preference is preserved while disabled");
assert.deepEqual(sortedMovieIDs(), ["n", "term", "abyss", "bug", "matrix", "a", "b", "c", "alps", "t"], "non-title sort ignores the checkbox preference");

document.getElementById("sortField").value = "title";
document.getElementById("sortField").dispatchEvent(new FakeEvent("change"));
assert.equal(document.getElementById("ignoreLeadingThe").disabled, false, "ignore-leading-the is re-enabled for title sorts");
assert.equal(document.getElementById("ignoreLeadingA").disabled, false, "ignore-leading-a is re-enabled for title sorts");
assert.equal(document.getElementById("ignoreLeadingThe").checked, true, "ignore-leading-the preference is remembered for title sorts");
assert.equal(document.getElementById("ignoreLeadingA").checked, true, "ignore-leading-a preference is remembered for title sorts");
assert.deepEqual(sortedMovieIDs(), ["n", "abyss", "a", "alps", "t", "b", "bug", "c", "matrix", "term"], "remembered preference applies when returning to title sort");

console.log("app regression passed");
