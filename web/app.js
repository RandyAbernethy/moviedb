// State and constants

const $ = (id) => document.getElementById(id);

let movies = [];
let current = null;
let sortAscending = true;
let columnWidths = defaultColumnWidths();

const maxCoverArtBytes = 20 * 1024 * 1024;
const allowedCoverTypes = new Set(["image/jpeg", "image/png", "image/gif", "image/webp"]);

function blankMovie() {
  return {
    title: "",
    format: $("format").value || "DVD",
    studio: "",
    directors: [],
    cast: [],
    producers: [],
    credits: {},
    genre: [],
    releaseDate: "",
    runtime: "",
    rating: "",
    myRating: "",
    synopsis: "",
    sourceUrl: "",
    amazonUrl: "",
    location: "",
    notes: "",
    externalIds: {},
    imagePath: "",
  };
}

const searchFields = [
  ["title", "Title"],
  ["format", "Format"],
  ["studio", "Studio"],
  ["directors", "Director"],
  ["cast", "Cast"],
  ["producers", "Producer"],
  ["credits", "Credits"],
  ["genre", "Genre"],
  ["releaseDate", "Release date"],
  ["runtime", "Runtime"],
  ["rating", "MPA Rating"],
  ["myRating", "MyRating"],
  ["synopsis", "Synopsis"],
  ["sourceUrl", "Source URL"],
  ["amazonUrl", "Amazon URL"],
  ["location", "Location"],
  ["notes", "Notes"],
  ["externalIds", "External IDs"],
];

const sortFields = [
  ["id", "ID"],
  ...searchFields,
  ["imagePath", "Image path"],
  ["createdAt", "Created"],
  ["updatedAt", "Updated"],
];

const fields = {
  title: $("title"),
  format: $("movieFormat"),
  studio: $("studio"),
  directors: $("directors"),
  cast: $("cast"),
  producers: $("producers"),
  genre: $("genre"),
  releaseDate: $("releaseDate"),
  runtime: $("runtime"),
  rating: $("rating"),
  myRating: $("myRating"),
  synopsis: $("synopsis"),
  sourceUrl: $("sourceUrl"),
  amazonUrl: $("amazonUrl"),
  location: $("location"),
  notes: $("notes"),
};

const formBindings = [
  ["title", fields.title, "", textValue, writeText],
  ["format", fields.format, "DVD", rawValue, writeText],
  ["studio", fields.studio, "", textValue, writeText],
  ["directors", fields.directors, [], csv, writeList],
  ["cast", fields.cast, [], csv, writeList],
  ["producers", fields.producers, [], csv, writeList],
  ["genre", fields.genre, [], csv, writeList],
  ["releaseDate", fields.releaseDate, "", textValue, writeText],
  ["runtime", fields.runtime, "", textValue, writeText],
  ["rating", fields.rating, "", textValue, writeText],
  ["myRating", fields.myRating, "", rawValue, writeText],
  ["synopsis", fields.synopsis, "", textValue, writeText],
  ["sourceUrl", fields.sourceUrl, "", textValue, writeText],
  ["amazonUrl", fields.amazonUrl, "", textValue, writeText],
  ["location", fields.location, "", textValue, writeText],
  ["notes", fields.notes, "", textValue, writeText],
];

// API

async function request(path, options = {}) {
  const response = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...options,
  });
  if (!response.ok) {
    const text = await response.text();
    let payload = {};
    try {
      payload = JSON.parse(text);
    } catch {
      payload = { error: text };
    }
    const error = new Error(payload.error || "Request failed");
    error.status = response.status;
    error.payload = payload;
    throw error;
  }
  if (response.status === 204) {
    return null;
  }
  return response.json();
}

async function loadMovies() {
  const params = new URLSearchParams({ q: $("search").value });
  const selected = selectedSearchFields();
  if (selected.length !== searchFields.length) {
    params.set("fields", selected.join(","));
  }
  movies = await request(`/api/movies?${params.toString()}`);
  await loadStats();
  renderResults();
}

async function loadStats() {
  const stats = await request("/api/stats");
  $("totalMovies").textContent = stats.totalMovies;
}

// Rendering

function renderSortFields() {
  const sort = $("sortField");
  sort.replaceChildren();
  for (const [value, label] of sortFields) {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = label;
    sort.appendChild(option);
  }
  sort.value = "title";
}

function renderSearchFields() {
  const box = $("fieldList");
  box.replaceChildren();
  for (const [value, label] of searchFields) {
    const item = document.createElement("label");
    item.className = "field-option";
    const input = document.createElement("input");
    input.type = "checkbox";
    input.value = value;
    input.checked = true;
    const span = document.createElement("span");
    span.textContent = label;
    item.append(input, " ", span);
    input.addEventListener("change", loadMovies);
    box.appendChild(item);
  }
}

function selectedSearchFields() {
  return [...document.querySelectorAll("#fieldList input:checked")].map((input) => input.value);
}

function setAllSearchFields(checked) {
  for (const input of document.querySelectorAll("#fieldList input")) {
    input.checked = checked;
  }
  loadMovies();
}

function renderResults() {
  const box = $("results");
  box.replaceChildren();
  $("resultCount").textContent = `${movies.length} match${movies.length === 1 ? "" : "es"}`;
  if (!movies.length) {
    const empty = document.createElement("p");
    empty.className = "status";
    empty.textContent = "No matching movies.";
    box.appendChild(empty);
    return;
  }
  for (const movie of sortedMovies()) {
    const button = document.createElement("button");
    button.className = `result${current && current.id === movie.id ? " active" : ""}`;
    button.type = "button";
    button.dataset.movieId = movie.id;
    const title = document.createElement("strong");
    title.textContent = movie.title || "Untitled";
    const meta = document.createElement("span");
    meta.textContent = [movie.format, movie.releaseDate, (movie.genre || []).join(", ")].filter(Boolean).join(" - ");
    button.append(title, meta);
    button.addEventListener("click", () => selectMovie(movie, { focusResult: true }));
    button.addEventListener("keydown", handleResultKeydown);
    box.appendChild(button);
  }
}

function sortedMovies() {
  const field = $("sortField").value || "title";
  return [...movies].sort((left, right) => {
    const comparison = compareValues(sortValue(left, field), sortValue(right, field));
    return sortAscending ? comparison : -comparison;
  });
}

function sortValue(movie, field) {
  const value = movie[field];
  if (Array.isArray(value)) {
    return value.join(", ");
  }
  if (field === "credits" || field === "externalIds") {
    return objectText(value);
  }
  return value || "";
}

function objectText(value) {
  if (!value) {
    return "";
  }
  return Object.entries(value)
    .map(([key, entry]) => `${key} ${entry}`)
    .sort()
    .join(" ");
}

function compareValues(left, right) {
  const leftDate = Date.parse(left);
  const rightDate = Date.parse(right);
  if (!Number.isNaN(leftDate) && !Number.isNaN(rightDate)) {
    return leftDate - rightDate;
  }
  return String(left).localeCompare(String(right), undefined, { numeric: true, sensitivity: "base" });
}

async function openMovie(id, options = {}) {
  const localMovie = movies.find((movie) => movie.id === id);
  if (localMovie) {
    selectMovie(localMovie, options);
    return;
  }
  const movie = await request(`/api/movies/${id}`);
  selectMovie(movie, options);
}

function selectMovie(movie, options = {}) {
  current = movie;
  $("empty").classList.add("hidden");
  $("movieForm").classList.remove("hidden");
  fillForm(current);
  renderResults();
  if (options.focusResult) {
    focusMovieResult(movie.id);
  }
}

function handleResultKeydown(event) {
  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") {
    return;
  }
  event.preventDefault();
  event.stopPropagation();
  const ordered = sortedMovies();
  const activeID = event.currentTarget.dataset.movieId || (current && current.id);
  const currentIndex = ordered.findIndex((movie) => movie.id === activeID);
  if (currentIndex === -1) {
    return;
  }
  const direction = event.key === "ArrowDown" ? 1 : -1;
  const nextIndex = Math.min(ordered.length - 1, Math.max(0, currentIndex + direction));
  if (nextIndex !== currentIndex) {
    selectMovie(ordered[nextIndex], { focusResult: true });
  }
}

function focusMovieResult(id) {
  const button = document.querySelector(`.result[data-movie-id="${CSS.escape(id)}"]`);
  if (!button) {
    return;
  }
  button.focus();
  button.scrollIntoView({ block: "nearest" });
}

// Form state

function fillForm(movie) {
  for (const [key, field, fallback, , write] of formBindings) {
    write(field, movie[key] ?? fallback);
  }
  const posterPath = safeImagePath(movie.imagePath);
  $("poster").src = posterPath;
  $("poster").hidden = !posterPath;
  $("posterTarget").classList.toggle("empty", !posterPath);
  const isSaved = isSavedMovie(movie);
  $("refreshButton").disabled = !canRefreshMovie(movie);
  $("deleteButton").disabled = !isSaved;
  $("coverArt").disabled = !isSaved;
  $("deleteCoverArt").disabled = !isSaved || !posterPath;
  $("posterTarget").classList.toggle("disabled", !isSaved);
  const coverUpload = $("coverArt").closest ? $("coverArt").closest(".cover-upload") : null;
  if (coverUpload) {
    coverUpload.classList.toggle("disabled", !isSaved);
  }
  $("coverArt").value = "";
  setCoverStatus("");
}

function isSavedMovie(movie) {
  return Boolean(movie && movie.id);
}

function canRefreshMovie(movie) {
  if (!movie) {
    return false;
  }
  return isSavedMovie(movie) || Boolean((fields.title.value || movie.title || "").trim());
}

function readForm() {
  const movie = { ...current };
  for (const [key, field, , read] of formBindings) {
    movie[key] = read(field.value);
  }
  return movie;
}

function textValue(value) {
  return value.trim();
}

function rawValue(value) {
  return value;
}

function csv(value) {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

function writeText(field, value) {
  field.value = value || "";
}

function writeList(field, value) {
  field.value = (value || []).join(", ");
}

function safeImagePath(path) {
  if (typeof path !== "string") {
    return "";
  }
  return /^\/images\/[A-Za-z0-9][A-Za-z0-9._-]*\.(?:gif|jfif|jpe?g|png|webp)$/i.test(path) && !path.includes("..")
    ? path
    : "";
}

function setStatus(message) {
  $("status").textContent = message;
}

function setCoverStatus(message) {
  $("coverStatus").textContent = message;
}

// Add/import flow

$("addForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const titles = $("titles").value.split(/\n|;/).map((title) => title.trim()).filter(Boolean);
  if (!titles.length) return;
  const button = event.submitter;
  button.disabled = true;
  setStatus(`Adding ${titles.length} movie${titles.length === 1 ? "" : "s"}...`);
  try {
    const added = [];
    for (const title of titles) {
      setStatus(`Adding ${title}...`);
      const saved = await addOneMovie(title);
      if (saved) {
        added.push(saved);
      }
    }
    $("titles").value = "";
    setStatus(`Added ${added.length} movie${added.length === 1 ? "" : "s"}.`);
    await loadMovies();
    if (added[0]) {
      await openMovie(added[0].id);
    }
  } catch (error) {
    setStatus(error.message);
  } finally {
    button.disabled = false;
  }
});

async function addOneMovie(title, duplicatePolicy = "") {
  const candidate = await chooseMovieCandidate(title);
  if (!candidate) {
    return null;
  }
  return saveMovieCandidate(candidate.movie, duplicatePolicy);
}

async function saveMovieCandidate(movie, duplicatePolicy = "") {
  let policy = duplicatePolicy;
  for (;;) {
    try {
      const added = await request("/api/movies", {
        method: "POST",
        body: JSON.stringify({ movie, format: $("format").value, duplicatePolicy: policy }),
      });
      return added[0] || null;
    } catch (error) {
      if (error.status !== 409 || !error.payload) {
        throw error;
      }
      policy = chooseDuplicatePolicy(error.payload.existing, error.payload.candidate);
      if (policy === "cancel") {
        return null;
      }
    }
  }
}

async function chooseMovieCandidate(title, format = $("format").value) {
  const candidates = await request("/api/lookup", {
    method: "POST",
    body: JSON.stringify({ title, format }),
  });
  const exact = candidates.filter((candidate) => candidate.matchType === "exact");
  if (exact.length === 1) {
    return exact[0];
  }
  if (exact.length > 1) {
    return promptForCandidate(title, exact, "Multiple exact title matches found");
  }
  return promptForCandidate(title, candidates, "Review approximate title matches");
}

function promptForCandidate(title, candidates, heading) {
  if (!candidates.length) {
    alert(`No matches found for ${title}.`);
    return null;
  }
  const lines = [
    `${heading} for: ${title}`,
    "",
    "Enter a number to add that movie, or leave blank/cancel to skip.",
    "",
    ...candidates.map((candidate, index) => {
      const movie = candidate.movie;
      const year = movie.releaseDate ? ` (${movie.releaseDate})` : "";
      const provider = candidate.provider ? ` - ${candidate.provider}` : "";
      const details = candidate.description ? ` - ${candidate.description}` : "";
      return `${index + 1}. ${movie.title || "Untitled"}${year}${provider}${details}`;
    }),
  ];
  const answer = prompt(lines.join("\n"), candidates.length === 1 ? "1" : "");
  const index = Number.parseInt((answer || "").trim(), 10);
  if (!Number.isInteger(index) || index < 1 || index > candidates.length) {
    return null;
  }
  return candidates[index - 1];
}

function chooseDuplicatePolicy(existing, candidate) {
  const answer = prompt(
    [
      `Duplicate movie found: ${candidate.title || existing.title}`,
      "",
      `Existing: ${existing.title || "Untitled"}${existing.releaseDate ? ` (${existing.releaseDate})` : ""}`,
      `New: ${candidate.title || "Untitled"}${candidate.releaseDate ? ` (${candidate.releaseDate})` : ""}`,
      "",
      "Choose an option:",
      "1. Cancel - abort adding the new record",
      "2. Merge New - copy new data into the existing record",
      "3. Merge Old - copy old data into the new record",
      "4. Overwrite - delete old record and add new record",
    ].join("\n"),
    "1"
  );
  switch ((answer || "1").trim()) {
    case "2":
      return "merge_new";
    case "3":
      return "merge_old";
    case "4":
      return "overwrite";
    case "1":
    default:
      return "cancel";
  }
}

// Event binding

$("search").addEventListener("input", () => {
  clearTimeout(window.searchTimer);
  window.searchTimer = setTimeout(loadMovies, 120);
});

$("selectAllFields").addEventListener("click", () => setAllSearchFields(true));
$("clearFields").addEventListener("click", () => setAllSearchFields(false));
$("results").addEventListener("keydown", handleResultKeydown);
$("sortField").addEventListener("change", renderResults);
$("sortDirection").addEventListener("click", () => {
  sortAscending = !sortAscending;
  $("sortDirection").textContent = sortAscending ? "A-Z" : "Z-A";
  renderResults();
});

$("movieForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  if (!current) return;
  const movie = readForm();
  if (!movie.title) {
    setStatus("Enter a title before saving the new movie.");
    fields.title.focus();
    return;
  }
  try {
    if (current.id) {
      current = await request(`/api/movies/${current.id}`, {
        method: "PUT",
        body: JSON.stringify(movie),
      });
    } else {
      const added = await request("/api/movies", {
        method: "POST",
        body: JSON.stringify({ movie }),
      });
      current = added[0] || null;
    }
    if (current) {
      fillForm(current);
    }
    await loadMovies();
    setStatus(`Saved ${current ? current.title : "movie"}.`);
  } catch (error) {
    if (error.status === 409) {
      setStatus("That title already exists. Enter a different title or a different release date before saving.");
      fields.releaseDate.focus();
      return;
    }
    setStatus(error.message);
  }
});

function startNewMovie() {
  selectMovie(blankMovie());
  setStatus("New blank movie. Enter a unique title or title/release-date, then click Save Changes.");
  fields.title.focus();
}

$("newButton").addEventListener("click", startNewMovie);
$("emptyNewButton").addEventListener("click", startNewMovie);

$("refreshButton").addEventListener("click", async () => {
  if (!current) return;
  const draft = readForm();
  if (!draft.title) {
    setStatus("Enter a title before updating from source.");
    fields.title.focus();
    return;
  }
  setStatus(`Loading source updates for ${draft.title}...`);
  try {
    if (current.id) {
      current = await request(`/api/movies/${current.id}/refresh`, { method: "POST" });
    } else {
      const candidate = await chooseMovieCandidate(draft.title, draft.format);
      if (!candidate) {
        setStatus(`No source update selected for ${draft.title}.`);
        return;
      }
      current = mergeDraftWithSource(draft, candidate.movie);
    }
    fillForm(current);
    setStatus(`Loaded source updates for ${current.title}. Click "Save changes" to write them to the database.`);
  } catch (error) {
    setStatus(error.message);
  }
});

fields.title.addEventListener("input", () => {
  if (current && !current.id) {
    $("refreshButton").disabled = !canRefreshMovie(current);
  }
});

function mergeDraftWithSource(draft, source) {
  const merged = {
    ...source,
    id: "",
    createdAt: undefined,
    updatedAt: undefined,
    format: draft.format || source.format,
    location: draft.location,
    notes: draft.notes,
    myRating: draft.myRating,
    amazonUrl: draft.amazonUrl || source.amazonUrl,
  };
  return merged;
}

// Cover art

$("coverArt").addEventListener("change", async (event) => {
  if (!current || !current.id || !event.target.files.length) {
    return;
  }
  await uploadCoverArt(event.target.files[0]);
});

for (const target of [$("posterTarget"), $("poster")]) {
  target.addEventListener("paste", handleCoverPaste);
}

$("posterTarget").addEventListener("beforeinput", (event) => {
  event.preventDefault();
});

document.addEventListener("paste", (event) => {
  if (document.activeElement === $("posterTarget") || $("posterTarget").contains(document.activeElement)) {
    handleCoverPaste(event);
  }
});

$("posterTarget").addEventListener("dragover", (event) => {
  event.preventDefault();
  $("posterTarget").classList.add("drop-ready");
});

$("posterTarget").addEventListener("dragleave", () => {
  $("posterTarget").classList.remove("drop-ready");
});

$("posterTarget").addEventListener("drop", async (event) => {
  event.preventDefault();
  $("posterTarget").classList.remove("drop-ready");
  const file = [...event.dataTransfer.files].find((item) => item.type.startsWith("image/"));
  if (file) {
    await uploadCoverArt(file);
  }
});

function coverFileFromClipboard(event) {
  const files = [...(event.clipboardData?.files || [])];
  const itemFiles = [...(event.clipboardData?.items || [])]
    .filter((item) => item.kind === "file")
    .map((item) => item.getAsFile())
    .filter(Boolean);
  return [...files, ...itemFiles].find((item) => item.type.startsWith("image/"));
}

async function handleCoverPaste(event) {
  if (!current || !current.id) {
    return;
  }
  const file = coverFileFromClipboard(event);
  if (file) {
    event.preventDefault();
    await uploadCoverArt(file);
  }
}

async function uploadCoverArt(file) {
  if (!current || !current.id || !file) {
    return;
  }
  const validationError = validateCoverFile(file);
  if (validationError) {
    setCoverStatus(validationError);
    return;
  }
  const body = new FormData();
  body.append("cover", file);
  setCoverStatus("Uploading cover art...");
  try {
    const response = await fetch(`/api/movies/${current.id}/image`, {
      method: "POST",
      body,
    });
    if (!response.ok) {
      throw new Error(await response.text());
    }
    current = await response.json();
    fillForm(current);
    renderResults();
    setCoverStatus("Cover art updated.");
  } catch (error) {
    setCoverStatus(error.message);
  }
}

function validateCoverFile(file) {
  if (file.size > maxCoverArtBytes) {
    return "Cover art must be 20 MB or smaller.";
  }
  if (file.type && !allowedCoverTypes.has(file.type)) {
    return "Cover art must be JPEG, PNG, GIF, or WebP.";
  }
  return "";
}

$("deleteCoverArt").addEventListener("click", async () => {
  if (!current || !current.id || !current.imagePath) {
    return;
  }
  if (!confirm(`Delete cover art for ${current.title}?`)) {
    return;
  }
  setCoverStatus("Deleting cover art...");
  try {
    const response = await fetch(`/api/movies/${current.id}/image`, {
      method: "DELETE",
    });
    if (!response.ok) {
      throw new Error(await response.text());
    }
    current = await response.json();
    fillForm(current);
    renderResults();
    setCoverStatus("Cover art deleted.");
    $("posterTarget").focus();
  } catch (error) {
    setCoverStatus(error.message);
  }
});

$("deleteButton").addEventListener("click", async () => {
  if (!current || !current.id || !confirm(`Delete ${current.title}?`)) return;
  await request(`/api/movies/${current.id}`, { method: "DELETE" });
  current = null;
  $("movieForm").classList.add("hidden");
  $("empty").classList.remove("hidden");
  await loadMovies();
});

// Layout

function defaultColumnWidths() {
  return { left: 320, middle: 320 };
}

function applyColumnWidths() {
  const app = document.querySelector(".app");
  if (window.innerWidth <= 800) {
    app.style.gridTemplateColumns = "";
    return;
  }
  app.style.gridTemplateColumns = `${columnWidths.left}px 6px ${columnWidths.middle}px 6px minmax(360px, 1fr)`;
}

function initColumnResizers() {
  applyColumnWidths();
  window.addEventListener("resize", applyColumnWidths);
  for (const handle of document.querySelectorAll(".resizer")) {
    handle.addEventListener("pointerdown", (event) => {
      if (window.innerWidth <= 800) {
        return;
      }
      event.preventDefault();
      const kind = handle.dataset.resizer;
      const startX = event.clientX;
      const startLeft = columnWidths.left;
      const startMiddle = columnWidths.middle;
      handle.setPointerCapture(event.pointerId);
      handle.classList.add("active");
      document.body.classList.add("resizing");

      const move = (moveEvent) => {
        const delta = moveEvent.clientX - startX;
        if (kind === "left") {
          columnWidths.left = clamp(startLeft + delta, 240, 520);
        } else {
          columnWidths.middle = clamp(startMiddle + delta, 220, 620);
        }
        applyColumnWidths();
      };

      const stop = () => {
        handle.classList.remove("active");
        document.body.classList.remove("resizing");
        handle.removeEventListener("pointermove", move);
        handle.removeEventListener("pointerup", stop);
        handle.removeEventListener("pointercancel", stop);
      };

      handle.addEventListener("pointermove", move);
      handle.addEventListener("pointerup", stop);
      handle.addEventListener("pointercancel", stop);
    });
  }
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

// Init

renderSearchFields();
renderSortFields();
initColumnResizers();
loadMovies();
