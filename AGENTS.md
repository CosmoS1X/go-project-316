# AGENTS.md

## О проекте

Консольный парсер структуры сайта на Go. Модуль временно называется `code` (переименуем к релизу). Бинарник — `hexlet-go-crawler`.

## Конвенции

- Все комментарии в коде — **на английском**.
- CLI строится на `github.com/urfave/cli/v3`.
- Любая работа с HTTP — только через `*http.Client`, переданный в `Options.HTTPClient`.
  Запрещены `http.Get`, `http.DefaultClient` и неявные клиенты.
- В продакшен-коде `main` создаёт стандартный `&http.Client{}`; тесты передают
  тестовый клиент (`httptest` / кастомный `http.RoundTripper`).
- `Analyze(ctx, Options)` — тонкая точка входа: валидация, запуск обхода, сериализация.
  Состояние обхода (клиент, опции, `visited`) живёт в отдельной структуре с методами.
- Тесты — во внешних пакетах `_test` (включён линтер `testpackage`), на `testify`:
  `require` для обязательных проверок, `assert` для остальных.
- Стремиться к максимальному покрытию, включая негативные сценарии и обработку ошибок.
  Если покрытие участка нецелесообразно (glue-код `main`, недостижимые защитные ветки,
  логика сторонних пакетов), явно указывать это с обоснованием в отчёте о задаче.
- Сетевые ошибки не считаются ошибкой запуска: они попадают в отчёт, код выхода — 0.
  Код 1 — только при некорректных аргументах/опциях.
- Линтер: `golangci-lint` (`2.15.0`), конфиг `.golangci.yml`. При ложном срабатывании
  сначала обсуждаем и правим конфиг, а не замалчиваем код.
- Когда `Options` вырастет и `gocritic:hugeParam` начнёт ругаться на
  `Analyze(ctx, Options)` (порог 80 байт), добавить `hugeParam` в
  `gocritic.disabled-checks`.
- Makefile: `build`, `test`, `cover` (профиль + сводка), `cover-show` (открыть отчёт
  в браузере), `run`, `fmt`, `lint` (зависит от `fmt`), `tidy`, `clean`.
- Перед завершением задачи обязательно: `make test && make lint`.

## Коммиты

- Коммиты делаются только по явному запросу.
- Conventional Commits: `feat:`, `fix:`, `chore:`, `test:`, `docs:`, `ci:`.
- Commit-сообщения — на английском.
- Коммиты небольшие и логически раздельные; каждый коммит должен собираться
  (`go build ./...` или `make build`).
- Примеры: `feat: add depth flag`, `fix: keep exit code zero on network errors`,
  `test: cover invalid urls`, `docs: describe commit conventions`.

## Целевая структура

```
.
├── cmd/hexlet-go-crawler/
│   └── main.go              # создаёт &http.Client{}, запускает cli.Command
├── internal/app/
│   ├── app.go               # urfave/cli: аргументы/флаги -> crawler.Options
│   └── app_test.go
├── crawler/
│   ├── analyze.go           # Analyze(ctx, Options) ([]byte, error)
│   ├── options.go           # Options, sentinel-ошибки, валидация
│   ├── crawler.go           # структура обхода, Run/visit/visited
│   ├── fetch.go             # HTTP-запрос, retries, timeout, User-Agent, rate limit
│   ├── report.go            # Report/Page, JSON-сериализация
│   └── *_test.go
├── Makefile
├── .gitignore
└── .golangci.yml
```

## Публичный API

```go
func Analyze(ctx context.Context, opts Options) ([]byte, error)
```

Sentinel-ошибки: `ErrInvalidURL`, `ErrNilHTTPClient`, `ErrInvalidOptions`.

## Options

Текущие поля:

- `URL string`
- `Depth int`
- `Timeout time.Duration`
- `IndentJSON bool`
- `HTTPClient *http.Client`

Добавить на следующих шагах:

- `Retries int`
- `Delay time.Duration`
- `UserAgent string`
- `Concurrency int`
- `RPS float64` (приоритет над `Delay`)

## JSON-отчёт

```json
{
  "root_url": "https://example.com",
  "depth": 1,
  "generated_at": "2026-10-22T12:00:00Z",
  "pages": [
    {
      "url": "https://example.com",
      "depth": 0,
      "http_status": 200,
      "status": "ok",
      "error": ""
    }
  ]
}
```

- `generated_at` — UTC с точностью до секунды.
- `status`: `ok` при HTTP < 400, иначе `error`.
- `http_status`: 0 при сетевой ошибке.
- `error`: пустая строка при успехе.

## CLI

Текущий шаг: `hexlet-go-crawler <url>` (обязательный аргумент) + `--help`.

Целевой набор флагов:

| Флаг | Тип | По умолчанию |
| --- | --- | --- |
| `--depth` | int | 10 |
| `--retries` | int | 1 |
| `--delay` | duration | 0s |
| `--timeout` | duration | 15s |
| `--rps` | float | 0 |
| `--user-agent` | string | "" |
| `--workers` | int | 4 |

## Роадмап

1. **Сделано:** черновик отчёта, `make run URL=...`, инъекция клиента, код 0 при сетевых ошибках, тесты на `testify`.
2. Полный `Options` и CLI-флаги из таблицы.
3. Retries, per-request timeout, `User-Agent`, rate limiting (`golang.org/x/time/rate`).
4. BFS-обход: `visited`, `Depth`, `Concurrency`/`workers`, извлечение ссылок (`golang.org/x/net/html`).
5. SEO-метрики страниц (title, description, h1, canonical и т.п.).
6. Расширение тестов и финальная проверка линтером.
