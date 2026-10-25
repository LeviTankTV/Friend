<!-- Measurements over vibes: describe what you measured. -->

**What changes** —

**Checks**
- [ ] `cd server && go vet ./... && go test ./...` green (Windows vet WASAPI
      warning is documented in PLAN.md)
- [ ] `cd desktop && npx electron . --selftest` → exit 0
- [ ] Behavior change covered: Go test in the owning package and/or a new
      numbered self-test part in `desktop/main.js`
- [ ] User-visible strings added to both dictionaries
      (`web/js/lang/ru.js` + `en.js`, `server/internal/i18n` if server-side)
- [ ] Live-run findings recorded in PLAN.md (if anything surprised you)
