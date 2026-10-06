## What does this change?

<!-- A short description and the motivation. Link the issue: "Closes #123". -->

## How was it tested?

- [ ] `gofmt -l .` prints nothing, `go vet ./...` and `go test -race ./...` pass (backend, with `TEST_DATABASE_URL` set)
- [ ] `npm run build` passes (frontend)
- [ ] I ran the app and checked the change at phone width, light and dark (attach a screenshot for UI changes)
- [ ] For offline or upload changes: I tested with the network off and back on

## Checklist

- [ ] No secrets, real photos, coordinates or personal data are committed
- [ ] The AI's suggestion stays separate from what the person confirms
- [ ] New outside services or assets are open data/open source, with documented terms
- [ ] Prompt changes include the test photos and before/after results
- [ ] README/docs updated if behavior or configuration changed
- [ ] If AI tools helped, I reviewed and tested every line and mention it here
