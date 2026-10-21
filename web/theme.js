// Applies the saved theme before first paint; kept in its own file so the CSP needs no 'unsafe-inline' script.
try { var t = localStorage.getItem('mv.theme'); if (t) document.documentElement.dataset.theme = t; } catch (e) {}
