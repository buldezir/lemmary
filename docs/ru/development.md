# Среда разработки {#development-environment}

Поддерживаемый способ установки — опубликованный [образ Docker](/ru/self_hosting).
Это руководство предназначено только для контрибьюторов и тех, кто сознательно хочет собирать
или запускать Lemmary прямо на хосте.

## Требования {#prerequisites}

- Go 1.27+ с включённым cgo (набор инструментов C: `gcc` или `clang`)
- Node.js 20+
- [pnpm](https://pnpm.io/installation) 11+ (`npm install -g pnpm`)
- [poppler-utils](https://poppler.freedesktop.org/) для всей работы с PDF: `pdftoppm`
  (превью и миниатюры страниц), `pdfinfo` (число страниц), `pdftotext` (текст
  страниц), `pdfseparate` и `pdfunite` (разделение документов)

В macOS выполните `brew install poppler`. В Debian или Ubuntu выполните
`apt install poppler-utils`.

Эти зависимости хоста намеренно не являются требованиями для обычного
самостоятельного хостинга: они уже включены в образ Docker.

## FAISS {#faiss}

FAISS необходим для сборки бэкенда. Поиск построен на
[Bleve](https://github.com/blevesearch/bleve), поддержка векторов в котором — это cgo-привязка
к FAISS. Bleve исключает этот API при компиляции, если не задан тег сборки `vectors`,
а Lemmary всегда собирается с ним: один бинарный файл и один образ, с включённым векторным
поиском. Сборка без тега сразу останавливается в
`backend/internal/fulltext/vectors_required.go`.

Библиотека должна быть **форком FAISS от Bleve**. Пакета `libfaiss` из дистрибутива
недостаточно, потому что Go-привязка вызывает точки входа C, которые есть
только в форке. Закреплённый коммит задаётся в `scripts/faiss-build.sh`.
Это единственный источник истины, и он меняется только вслед за Bleve, согласно
таблице совместимости в `docs/vectors.md` Bleve.

Каждый из вариантов ниже также требует OpenBLAS и libgomp при компоновке бэкенда и
при его запуске (`apt install libopenblas0-pthread libgomp1`;
`libopenblas-dev` подтягивает их сам).

```bash
# Option 1 — install system-wide. One sudo, and nothing to set afterwards.
sudo apt install cmake ninja-build g++ libopenblas-dev
sudo scripts/faiss-build.sh --prefix /usr/local && sudo ldconfig

# Option 2 — install in your home directory. Same build, no root.
scripts/faiss-build.sh --prefix "$HOME/.local/faiss"

# Option 3 — export the artifacts from the Docker build. This needs no local
# cmake or compiler; the exported FAISS stage is only about 10 MB.
docker buildx build --target faiss --output type=local,dest=./.faiss .
mkdir -p "$HOME/.local/faiss" && cp -a .faiss/lib .faiss/include "$HOME/.local/faiss/"
```

Варианты 2 и 3 размещают FAISS вне стандартных путей компилятора и загрузчика:

```bash
export CGO_CFLAGS=-I$HOME/.local/faiss/include
export CGO_LDFLAGS=-L$HOME/.local/faiss/lib
export LD_LIBRARY_PATH=$HOME/.local/faiss/lib
```

`.envrc` репозитория задаёт все три переменные, если существует `~/.local/faiss`. С
[direnv](https://direnv.net) выполните `direnv allow`; он также экспортирует
`GOFLAGS=-tags=vectors`, благодаря чему в этом дереве работают простые `go build`, `go test` и gopls.
Без direnv передавайте `-tags vectors` или задайте его один раз через
`go env -w GOFLAGS=-tags=vectors`.

FAISS должен быть на хосте только для Go-команд, запускаемых там: обычных
`go build`/`go test` или проверки в режиме хоста. Обычный набор проверок
выполняется в Docker, где FAISS уже установлен.

В macOS выполните `brew install cmake ninja libomp openblas`, затем используйте вариант 1 или
2; скрипт сборки находит libomp из Homebrew автоматически.

## Запуск из исходного кода {#run-from-source}

Сначала скопируйте пример окружения. Параметры времени выполнения описаны в
[Руководстве по настройке](/ru/setup), а параметры, связанные с ИИ, — в
[ИИ-провайдерах и моделях](/ru/ai_providers).

```bash
cp .env.example .env
```

Запустите бэкенд:

```bash
cd backend
go run . serve --http=127.0.0.1:8090
```

При первом запуске миграции создают коллекции PocketBase:

- `tags`
- `correspondents`
- `document_types`
- `documents`
- `processing_jobs`
- `app_settings`
- `ai_providers`
- `outbound_emails`

Затем приложение открывает тот же
[мастер первоначальной настройки](/ru/setup#first-launch-setup-wizard), что и при установке
в Docker. `app_settings` и `ai_providers` заполняются из `.env` при первом
запуске.

Один раз соберите фронтенд и документацию, затем перезапустите бэкенд:

```bash
cd frontend
pnpm install --frozen-lockfile
pnpm run build
```

Это записывает приложение в `public/`, а документацию — в `public/docs/`; бэкенд
отдаёт и то и другое со своего адреса.

## Полезные команды {#useful-commands}

```bash
# Frontend production build (SPA -> ../public, docs -> ../public/docs)
cd frontend && pnpm run build

# Create or update an admin (PocketBase superuser + paired users account)
cd backend && go run . superuser upsert admin@example.com 'your-password'
```
