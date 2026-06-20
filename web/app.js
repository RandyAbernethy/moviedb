const $ = (id) => document.getElementById(id);

let movies = [];
let current = null;
let sortAscending = true;
let columnWidths = loadColumnWidths();

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
  synopsis: $("synopsis"),
  sourceUrl: $("sourceUrl"),
  amazonUrl: $("amazonUrl"),
  location: $("location"),
  notes: $("notes"),
};

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

function renderSortFields() {
  const sort = $("sortField");
  sort.innerHTML = "";
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
  box.innerHTML = "";
  for (const [value, label] of searchFields) {
    const item = document.createElement("label");
    item.className = "field-option";
    item.innerHTML = `<input type="checkbox" value="${value}" checked> <span>${label}</span>`;
    item.querySelector("input").addEventListener("change", loadMovies);
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
  box.innerHTML = "";
  $("resultCount").textContent = `${movies.length} match${movies.length === 1 ? "" : "es"}`;
  if (!movies.length) {
    box.innerHTML = `<p class="status">No matching movies.</p>`;
    return;
  }
  for (const movie of sortedMovies()) {
    const button = document.createElement("button");
    button.className = `result${current && current.id === movie.id ? " active" : ""}`;
    button.type = "button";
    button.dataset.movieId = movie.id;
    button.innerHTML = `
      <strong>${escapeHTML(movie.title || "Untitled")}</strong>
      <span>${escapeHTML([movie.format, movie.releaseDate, (movie.genre || []).join(", ")].filter(Boolean).join(" - "))}</span>
    `;
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

function fillForm(movie) {
  fields.title.value = movie.title || "";
  fields.format.value = movie.format || "DVD";
  fields.studio.value = movie.studio || "";
  fields.directors.value = (movie.directors || []).join(", ");
  fields.cast.value = (movie.cast || []).join(", ");
  fields.producers.value = (movie.producers || []).join(", ");
  fields.genre.value = (movie.genre || []).join(", ");
  fields.releaseDate.value = movie.releaseDate || "";
  fields.runtime.value = movie.runtime || "";
  fields.rating.value = movie.rating || "";
  fields.synopsis.value = movie.synopsis || "";
  fields.sourceUrl.value = movie.sourceUrl || "";
  fields.amazonUrl.value = movie.amazonUrl || "";
  fields.location.value = movie.location || "";
  fields.notes.value = movie.notes || "";
  $("poster").src = movie.imagePath || "";
  $("poster").style.visibility = movie.imagePath ? "visible" : "hidden";
}

function readForm() {
  return {
    ...current,
    title: fields.title.value.trim(),
    format: fields.format.value,
    studio: fields.studio.value.trim(),
    directors: csv(fields.directors.value),
    cast: csv(fields.cast.value),
    producers: csv(fields.producers.value),
    genre: csv(fields.genre.value),
    releaseDate: fields.releaseDate.value.trim(),
    runtime: fields.runtime.value.trim(),
    rating: fields.rating.value.trim(),
    synopsis: fields.synopsis.value.trim(),
    sourceUrl: fields.sourceUrl.value.trim(),
    amazonUrl: fields.amazonUrl.value.trim(),
    location: fields.location.value.trim(),
    notes: fields.notes.value.trim(),
  };
}

function csv(value) {
  return value.split(",").map((item) => item.trim()).filter(Boolean);
}

function escapeHTML(value) {
  return value.replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#039;",
  }[char]));
}

$("addForm").addEventListener("submit", async (event) => {
  event.preventDefault();
  const titles = $("titles").value.split(/\n|;/).map((title) => title.trim()).filter(Boolean);
  if (!titles.length) return;
  const button = event.submitter;
  button.disabled = true;
  $("status").textContent = `Adding ${titles.length} movie${titles.length === 1 ? "" : "s"}...`;
  try {
    const added = [];
    for (const title of titles) {
      $("status").textContent = `Adding ${title}...`;
      const saved = await addOneMovie(title);
      if (saved) {
        added.push(saved);
      }
    }
    $("titles").value = "";
    $("status").textContent = `Added ${added.length} movie${added.length === 1 ? "" : "s"}.`;
    await loadMovies();
    if (added[0]) {
      await openMovie(added[0].id);
    }
  } catch (error) {
    $("status").textContent = error.message;
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
  try {
    const added = await request("/api/movies", {
      method: "POST",
      body: JSON.stringify({ movie, format: $("format").value, duplicatePolicy }),
    });
    return added[0] || null;
  } catch (error) {
    if (error.status !== 409 || !error.payload) {
      throw error;
    }
    const policy = chooseDuplicatePolicy(error.payload.existing, error.payload.candidate);
    if (policy === "cancel") {
      return null;
    }
    return saveMovieCandidate(movie, policy);
  }
}

async function chooseMovieCandidate(title) {
  const candidates = await request("/api/lookup", {
    method: "POST",
    body: JSON.stringify({ title, format: $("format").value }),
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
  current = await request(`/api/movies/${current.id}`, {
    method: "PUT",
    body: JSON.stringify(readForm()),
  });
  await loadMovies();
});

$("deleteButton").addEventListener("click", async () => {
  if (!current || !confirm(`Delete ${current.title}?`)) return;
  await request(`/api/movies/${current.id}`, { method: "DELETE" });
  current = null;
  $("movieForm").classList.add("hidden");
  $("empty").classList.remove("hidden");
  await loadMovies();
});

function loadColumnWidths() {
  try {
    const saved = JSON.parse(localStorage.getItem("moviedbColumnWidths") || "{}");
    return {
      left: clamp(Number(saved.left) || 320, 240, 520),
      middle: clamp(Number(saved.middle) || 320, 220, 620),
    };
  } catch {
    return { left: 320, middle: 320 };
  }
}

function saveColumnWidths() {
  localStorage.setItem("moviedbColumnWidths", JSON.stringify(columnWidths));
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
        saveColumnWidths();
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

renderSearchFields();
renderSortFields();
initColumnResizers();
loadMovies();
