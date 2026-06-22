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
  "titles", "format", "status", "selectAllFields", "clearFields", "deleteButton",
  "totalMovies", "coverArt", "coverStatus", "posterTarget", "deleteCoverArt", "refreshButton",
  "newButton", "emptyNewButton", "ignoreLeadingThe", "ignoreLeadingTheLabel",
];
for (const id of requiredIDs) document.getElementById(id);
const app = document.createElement("main");
app.className = "app";
document.body.appendChild(app);
document.body.appendChild(document.getElementById("results"));

const movies = [
  { id: "n", title: "2001: A Space Odyssey", format: "Blu-ray", genre: [], releaseDate: "1968" },
  { id: "a", title: "Alpha", format: "DVD", genre: [], releaseDate: "2001" },
  { id: "abyss", title: "The Abyss", format: "DVD", genre: [], releaseDate: "1989" },
  { id: "t", title: "The Artist", format: "DVD", genre: [], releaseDate: "2011" },
  { id: "b", title: "Bravo", format: "DVD", genre: [], releaseDate: "2002" },
  { id: "c", title: "Charlie", format: "DVD", genre: [], releaseDate: "2003" },
  { id: "term", title: "Terminator", format: "DVD", genre: [], releaseDate: "1984" },
];
const requests = [];
const objectURLs = [];
const revokedObjectURLs = [];
const objectURLAPI = {
  createObjectURL(file) {
    const url = `blob:${file.name}`;
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
    json: async () => payload,
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
      return jsonResponse([{
        matchType: "exact",
        movie: {
          id: "lookup-id",
          title: "Source Movie",
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

document.getElementById("emptyNewButton").dispatchEvent(new FakeEvent("click"));
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
assert.deepEqual(revokedObjectURLs, ["blob:draft-cover.png"], "temporary draft cover preview URL is revoked after upload");

context.openMovie("a", { focusResult: true });
assert.equal(document.getElementById("title").value, "Alpha", "opening first movie populates detail view");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "a", "first movie is active");

const down = new FakeEvent("keydown", { key: "ArrowDown" });
results.dispatchEvent(down);
assert.equal(down.defaultPrevented, true, "ArrowDown is handled by the results list");
assert.equal(document.getElementById("title").value, "Bravo", "ArrowDown opens next movie in detail view");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "b", "ArrowDown moves active tile");

const up = new FakeEvent("keydown", { key: "ArrowUp" });
results.dispatchEvent(up);
assert.equal(up.defaultPrevented, true, "ArrowUp is handled by the results list");
assert.equal(document.getElementById("title").value, "Alpha", "ArrowUp opens previous movie in detail view");
assert.equal(document.querySelector(".result.active")?.dataset.movieId, "a", "ArrowUp moves active tile");

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
assert.deepEqual(sortedMovieIDs(), ["n", "abyss", "a", "t", "b", "c", "term"], "title sort can ignore leading The");

const ignoredArticleJump = new FakeEvent("keydown", { key: "t" });
results.dispatchEvent(ignoredArticleJump);
assert.equal(ignoredArticleJump.defaultPrevented, true, "letter jump still handles keys while ignoring leading The");
assert.equal(document.getElementById("title").value, "Terminator", "letter jump skips leading-The titles when ignore-leading-the is active");

document.getElementById("sortField").value = "releaseDate";
document.getElementById("sortField").dispatchEvent(new FakeEvent("change"));
assert.equal(document.getElementById("ignoreLeadingThe").disabled, true, "ignore-leading-the is disabled for non-title sorts");
assert.equal(document.getElementById("ignoreLeadingThe").checked, true, "ignore-leading-the preference is preserved while disabled");
assert.deepEqual(sortedMovieIDs(), ["n", "term", "abyss", "a", "b", "c", "t"], "non-title sort ignores the checkbox preference");

document.getElementById("sortField").value = "title";
document.getElementById("sortField").dispatchEvent(new FakeEvent("change"));
assert.equal(document.getElementById("ignoreLeadingThe").disabled, false, "ignore-leading-the is re-enabled for title sorts");
assert.equal(document.getElementById("ignoreLeadingThe").checked, true, "ignore-leading-the preference is remembered for title sorts");
assert.deepEqual(sortedMovieIDs(), ["n", "abyss", "a", "t", "b", "c", "term"], "remembered preference applies when returning to title sort");

console.log("keyboard navigation regression passed");
