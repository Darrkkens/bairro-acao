# Contributing to Bairro em Ação

Thanks for your interest! Bairro em Ação is a walk-first app for reporting problems in public spaces: a React + TypeScript PWA, a Go API, PostgreSQL, and an open-weight vision model (Gemma 3 4B) running locally through Ollama. Contributions of any size are welcome: bug reports, field-test results, prompt and category improvements, translations, tests, docs and features.

> 🇧🇷 Contribuições em português são bem-vindas. Issues e pull requests podem ser escritos em português ou inglês.

## Before you start

- Look for an existing issue, or open one to discuss larger changes before writing code.
- Issues labeled `good first issue` are scoped for newcomers.
- Please read the [Code of Conduct](CODE_OF_CONDUCT.md).

> **Hacktoberfest 2026:** pull requests no longer count toward Hacktoberfest rewards, so there is no PR quota to meet here. One thoughtful, tested contribution is worth more than many small ones. Low-effort or AI-generated PRs that their author did not review and test will be closed.

## Development setup

Requirements: Go 1.25+, Node.js 22+, Docker (PostgreSQL) and [Ollama](https://ollama.com) with `gemma3:4b`.

```sh
cp .env.example .env                      # set POSTGRES_PASSWORD and the same password in DATABASE_URL
docker compose up -d
ollama serve & ollama pull gemma3:4b
cd backend && go run ./cmd/server         # API on http://127.0.0.1:8090
cd frontend && npm install && npm run dev # app on http://localhost:5174
```

Recording and reviewing work without Ollama: points wait in the analysis queue and can be filled in by hand. To try it on a phone, see "Using it on a phone" in the README.

## Checks to run before opening a PR

```sh
cd backend
test -z "$(gofmt -l .)"
go vet ./...
TEST_DATABASE_URL="$DATABASE_URL" go test -race ./...   # without the variable, PostgreSQL tests are skipped

cd ../frontend
npm run build                                            # type-check + production build
```

CI runs the same checks, with a PostgreSQL service for the API tests.

## Guidelines

- **The AI suggests; people decide.** Keep the AI's suggestion and the person's confirmation separate. Never fill a report with unreviewed model output.
- **Offline first.** Anything recorded during a walk goes through the outbox (`frontend/src/outbox.ts`) and must survive no signal, a reload and a retried upload.
- **Privacy.** Photos and locations stay on the user's machine. New outside services must be open data or open source, receive as little as possible (see how coordinates are rounded in `places.ts`), and be documented in the README.
- **Prompt changes need evidence.** If you change `backend/internal/ai/prompt.go`, describe the photos you tested with and before/after results. Do not tune the prompt on a single photo and call it accuracy.
- **Mobile matters.** Test UI changes at phone width (390 px) in light and dark mode; touch targets stay at least 44 px.
- Match the surrounding code: Go standard library first, small packages, comments that explain *why*.

## AI-assisted contributions

AI tools are welcome as long as you understand, review and test every change you submit. Say so in the PR description.
