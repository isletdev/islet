// Stamps the chosen theme on <html> before the first paint, so the panel never
// flashes the wrong one.
//
// This lives in its own file rather than inline in index.html because the panel
// sends a Content-Security-Policy of default-src 'self' with no 'unsafe-inline'
// for scripts. An inline version is silently dropped, which used to make a saved
// theme look like it reset on every reload.
try {
  var t = localStorage.getItem("islet.theme");
  if (t !== "dark" && t !== "light") {
    t = window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  }
  document.documentElement.dataset.theme = t;
} catch (e) {
  document.documentElement.dataset.theme = "light";
}
