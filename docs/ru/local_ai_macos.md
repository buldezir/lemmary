# Локальный OCR и эмбеддинги на Mac с Apple Silicon {#local-ocr-and-embeddings-on-an-apple-silicon-mac}

[Локальный OCR](/ru/local_ocr) и [локальные эмбеддинги](/ru/local_embeddings)
работают на вашем собственном оборудовании, поэтому документы не покидают вашу сеть.
В этом руководстве оба сервиса запускаются на Mac с чипом серии M — нативно, под
launchd, с использованием GPU и Neural Engine компьютера. Сам Lemmary работает на
другом хосте (виртуальной машине, Linux-сервере, чём угодно с Docker) и обращается к
Mac по локальной сети.

Проверено на базовом Mac mini M4 (16 ГБ): оба сервиса работают бок о бок и быстро —
несколько секунд на отсканированную страницу и около 4 с на полный пакет
эмбеддингов. См. [Измеренная производительность](#measured-performance).

Lemmary не требует никаких изменений: для эмбеддингов используется его SDK
«Локальные эмбеддинги» (`local`, любой OpenAI-совместимый `/v1/embeddings`), а для
OCR — его SDK «Локальный OCR» (`docling`).

## Заполнители {#placeholders}

| Заполнитель | Значение | Пример |
|---|---|---|
| `<MAC_IP>` | Постоянный адрес Mac в локальной сети (если их несколько, используйте проводной) | `192.168.1.20` |
| `<USER>` | Пользователь macOS, от имени которого работают сервисы | `alice` |
| `com.example` | Префикс в формате обратного DNS для меток launchd | ваш собственный домен |

## Обзор {#overview}

| | Эмбеддинги | OCR |
|---|---|---|
| SDK в Lemmary | `local` (OpenAI-совместимый `/v1/embeddings`) | `docling` |
| Сервер | llama.cpp `llama-server` (Homebrew), Metal | docling-serve 1.35.0 (uv tool), MPS |
| Модель / движок | `BAAI/bge-m3`, F16 GGUF | Apple Vision через `ocrmac`, модели разметки и таблиц docling на MPS |
| Эндпоинт | `http://<MAC_IP>:8080/v1` | `http://<MAC_IP>:5001` |
| Аутентификация | нет | нет |
| LaunchAgent | `com.example.llama-embeddings` | `com.example.docling-serve` |
| Резидентная память (измерено) | ~4,5 ГБ | ~3,9 ГБ |
| Лог | `~/ai/logs/llama-embeddings.log` | `~/ai/logs/docling-serve.log` |

## Требования к хосту {#host-requirements}

- Mac с Apple Silicon. Эталонная машина — Mac mini M4 (4P + 6E ядер, 10-ядерный
  GPU) с 16 ГБ на macOS 26. При обоих загруженных сервисах 16 ГБ оставляют место
  для CI-раннера или чего-то подобного.
- Постоянный IP-адрес. Если у Mac есть и проводной адрес, и адрес Wi-Fi,
  используйте проводной; сервисы слушают на всех интерфейсах.
- Сервисы являются **LaunchAgent**, поэтому работают внутри сеанса входа
  пользователя. Для сервера без монитора включите автоматический вход для `<USER>`,
  запретите Mac уходить в сон и включите перезапуск после сбоя питания (Системные
  настройки → Энергосбережение).
- Homebrew.

## Почему именно так {#why-this-setup}

- **Нативно, а не в Docker.** Docker на macOS (Docker Desktop, OrbStack, Colima)
  запускает виртуальную машину Linux без доступа к GPU Apple, поэтому всё в
  контейнере работало бы на CPU.
- **llama.cpp, а не TEI, для эмбеддингов.** Compose-файлы Lemmary используют
  text-embeddings-inference. У TEI есть сборка с Metal
  (`brew install text-embeddings-inference`), но на эталонном Mac она выдавала
  около 1 тыс. токенов/с против примерно 4 тыс. токенов/с у llama.cpp на той же
  модели.
- **docling с Apple Vision для OCR.** Это SDK, с которым Lemmary уже умеет
  работать, поэтому адаптер не нужен. Движок `auto` в docling сам выбирает
  `ocrmac` на macOS.

## Структура {#layout}

```
~/ai/
  models/bge-m3-f16.gguf        embeddings model (1.1 GB)
  docling/                      docling-serve working directory
  embeddings/embtest.py         embeddings smoke test / benchmark
  logs/                         service logs
~/.local/share/uv/tools/docling-serve/   docling-serve venv (Python 3.12, ~2.1 GB)
~/.cache/huggingface/                     docling layout/table models (~0.5 GB)
~/Library/LaunchAgents/com.example.llama-embeddings.plist
~/Library/LaunchAgents/com.example.docling-serve.plist
```

## Установка {#installation}

### 1. Пакеты {#_1-packages}

```sh
brew install llama.cpp uv poppler     # poppler only for pdfinfo/pdfimages when testing
mkdir -p ~/ai/models ~/ai/logs ~/ai/docling ~/ai/embeddings
```

Если Xcode установлен, но его лицензия не принята, Homebrew будет жаловаться.
Бутылки всё равно устанавливаются; чтобы убрать предупреждение, один раз выполните
`sudo xcodebuild -license accept`.

### 2. Эмбеддинги: llama.cpp + bge-m3 {#_2-embeddings-llama-cpp-bge-m3}

```sh
curl -fL -o ~/ai/models/bge-m3-f16.gguf \
  https://huggingface.co/gpustack/bge-m3-GGUF/resolve/main/bge-m3-FP16.gguf
```

Создайте `~/Library/LaunchAgents/com.example.llama-embeddings.plist`. launchd не
раскрывает `~`, поэтому замените `<USER>` настоящим именем пользователя:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.example.llama-embeddings</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/homebrew/bin/llama-server</string>
    <string>-m</string><string>/Users/<USER>/ai/models/bge-m3-f16.gguf</string>
    <string>--alias</string><string>BAAI/bge-m3</string>
    <string>--embeddings</string>
    <string>--pooling</string><string>cls</string>
    <string>-ngl</string><string>99</string>
    <string>-fa</string><string>on</string>
    <string>-c</string><string>8192</string>
    <string>-b</string><string>8192</string>
    <string>-ub</string><string>8192</string>
    <string>-np</string><string>1</string>
    <string>--host</string><string>0.0.0.0</string>
    <string>--port</string><string>8080</string>
    <string>--no-webui</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>/Users/<USER>/ai/logs/llama-embeddings.log</string>
  <key>StandardErrorPath</key><string>/Users/<USER>/ai/logs/llama-embeddings.log</string>
</dict>
</plist>
```

Почему такие флаги:

- `--pooling cls`: плотный эмбеддинг bge-m3 — это токен CLS; затем llama.cpp
  нормализует его по L2, так же как TEI.
- `-ub 8192`: модель-энкодер должна уместить каждый вход в один микропакет. Lemmary
  ограничивает один вход 8000 символами, а это до примерно 5,7 тыс. токенов в
  плотном тексте CJK; окно bge-m3 — 8192. При меньшем `-ub` длинные входы завершаются
  ошибкой, а не превращаются в эмбеддинги.
- `-fa on`: flash attention не даёт материализоваться матрице внимания на 8192
  токена, а на Metal ещё и быстрее (около 4 тыс. токенов/с при 512 токенах и
  2,4 тыс. при 4 тыс., против 3,8 тыс. и 1,7 тыс. без него).
- `-np 1`: четыре слота были быстрее всего примерно на 5% на пакете из 64
  фрагментов.
- `--alias BAAI/bge-m3`: это имя модели, которое Lemmary отправляет и показывает.
- F16, а не Q8_0: та же скорость на Metal и ближе к исходным весам.

```sh
plutil -lint ~/Library/LaunchAgents/com.example.llama-embeddings.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.llama-embeddings.plist
curl -s localhost:8080/health        # {"status":"ok"}
```

### 3. OCR: docling-serve + ocrmac {#_3-ocr-docling-serve-ocrmac}

```sh
uv tool install --python 3.12 docling-serve==1.35.0 --with ocrmac
```

Python 3.12 выбран намеренно: есть сообщения о падениях docling на Python 3.14 на
Apple Silicon. Установленный torch должен сообщать, что `mps` доступен:

```sh
~/.local/share/uv/tools/docling-serve/bin/python -c 'import torch; print(torch.backends.mps.is_available())'
```

Создайте `~/Library/LaunchAgents/com.example.docling-serve.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>com.example.docling-serve</string>
  <key>ProgramArguments</key>
  <array>
    <string>/Users/<USER>/.local/bin/docling-serve</string>
    <string>run</string>
    <string>--host</string><string>0.0.0.0</string>
    <string>--port</string><string>5001</string>
  </array>
  <key>WorkingDirectory</key><string>/Users/<USER>/ai/docling</string>
  <key>EnvironmentVariables</key>
  <dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
    <key>DOCLING_SERVE_MAX_SYNC_WAIT</key><string>900</string>
    <key>DOCLING_SERVE_ENABLE_UI</key><string>false</string>
    <key>UVICORN_WORKERS</key><string>1</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Interactive</string>
  <key>StandardOutPath</key><string>/Users/<USER>/ai/logs/docling-serve.log</string>
  <key>StandardErrorPath</key><string>/Users/<USER>/ai/logs/docling-serve.log</string>
</dict>
</plist>
```

- `DOCLING_SERVE_MAX_SYNC_WAIT=900`: синхронный эндпоинт конвертации сдаётся по
  истечении этого времени, что бы ни запросил клиент. Держите значение выше
  `OCR_TIMEOUT_SEC` в Lemmary.
- `UVICORN_WORKERS=1`: второй воркер загрузил бы вторую копию моделей без прироста
  пропускной способности. Lemmary и так отправляет docling по одному запросу за раз.
- LaunchAgent (сеанс вошедшего пользователя), а не LaunchDaemon: фреймворк Vision
  от Apple надёжнее всего работает внутри пользовательского сеанса.

```sh
plutil -lint ~/Library/LaunchAgents/com.example.docling-serve.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/com.example.docling-serve.plist
curl -s localhost:5001/health        # {"status":"ok"}
curl -s -o /dev/null -w '%{http_code}\n' localhost:5001/ready   # 200 once models are loaded
```

Первая конвертация скачивает модели разметки и таблиц docling (около 0,5 ГБ) в
`~/.cache/huggingface` и занимает около 40 с. Последующие выполняются на прогретых
моделях. В логе должны появиться `Auto OCR model selected ocrmac.` и
`Accelerator device: 'mps'`.

## Настройка Lemmary {#lemmary-configuration}

На экземпляре, который уже загружался, настройки ИИ в `.env` игнорируются (они лишь
задают начальные значения при первой загрузке). Настройте всё в интерфейсе.

**Настройки → ИИ → Провайдеры**

- **Локальные эмбеддинги** (`local`): базовый URL `http://<MAC_IP>:8080/v1`, без
  API-ключа.
- **Локальный OCR** (`docling`): базовый URL `http://<MAC_IP>:5001`, без API-ключа.

**Настройки → ИИ → Модели**

- Эмбеддинги: `BAAI/bge-m3` — это значение подставляет список выбора для провайдера
  «Локальные эмбеддинги». Оно совпадает с `--alias` выше.
- OCR: привяжите провайдер docling и оставьте модель OCR **пустой**, что означает
  движок `auto` в docling и, следовательно, `ocrmac`. Явное указание `ocrmac` тоже
  работает.

**Таймауты:** увеличьте `OCR_TIMEOUT_SEC` со значения по умолчанию 90
примерно до 120. Страница-фотография на прогретых моделях обрабатывается 4–8 с, но длинный
документ или первый запрос после перезапуска занимают больше. Держите значение ниже
`DOCLING_SERVE_MAX_SYNC_WAIT` (900).

Для нового экземпляра эквивалентный `.env` выглядит так:

```sh
AI_EMBEDDING_SDK=local
AI_EMBEDDING_BASE_URL=http://<MAC_IP>:8080/v1
AI_EMBEDDING_MODEL=BAAI/bge-m3
OCR_SDK=docling
OCR_BASE_URL=http://<MAC_IP>:5001
OCR_TIMEOUT_SEC=120
```

Проверьте доступность изнутри контейнера Lemmary:

```sh
docker exec <lemmary-container> wget -qO- -T 5 http://<MAC_IP>:8080/health
docker exec <lemmary-container> wget -qO- -T 5 http://<MAC_IP>:5001/health
```

Смена модели или сервера эмбеддингов означает повторное построение эмбеддингов для
всего архива (см. [Смена модели](/ru/local_embeddings#changing-the-model)). Векторы
bge-m3 из llama.cpp близки к векторам TEI, но не совпадают побитово.

## Скрипт дымового теста и бенчмарка {#smoke-test-and-benchmark-script}

Сохраните это как `~/ai/embeddings/embtest.py` и запустите
`python3 ~/ai/embeddings/embtest.py http://127.0.0.1:8080`:

```python
import json, math, sys, time, urllib.request

URL = sys.argv[1]

def emb(inputs):
    req = urllib.request.Request(URL + "/v1/embeddings",
        data=json.dumps({"model": "BAAI/bge-m3", "input": inputs}).encode(),
        headers={"Content-Type": "application/json"})
    t = time.time()
    return json.load(urllib.request.urlopen(req, timeout=600)), time.time() - t

def cos(a, b):
    return sum(x * y for x, y in zip(a, b)) / math.sqrt(sum(x * x for x in a) * sum(y * y for y in b))

r, _ = emb(["The cat sleeps on the sofa.", "Die Katze schläft auf dem Sofa.",
            "Кошка спит на диване.", "Quarterly VAT return for 2024 filed with the tax office."])
v = [d["embedding"] for d in r["data"]]
print("dim", len(v[0]), "norm", round(math.sqrt(sum(x * x for x in v[0])), 4))
print("en-de", round(cos(v[0], v[1]), 3), "en-ru", round(cos(v[0], v[2]), 3), "en-unrelated", round(cos(v[0], v[3]), 3))

para = "Invoice number 4711 for electricity supply, billing period January to March, total amount 312.45 EUR including VAT, due within fourteen days. " * 6
r, dt = emb([para + str(i) for i in range(64)])
print("64 x ~%d tok: %.2fs" % (r["usage"]["prompt_tokens"] // 64, dt))
```

Ожидаемый результат: `dim 1024 norm 1.0`, en-de около 0,92, en-ru около 0,78,
несвязанный текст около 0,29.

## Измеренная производительность {#measured-performance}

Прогоны на прогретых моделях на эталонном Mac mini M4 (16 ГБ).

**Эмбеддинги (bge-m3)**

| Тест | llama.cpp, Metal | TEI 1.9.4, Metal |
|---|---|---|
| 64 фрагмента × ~231 токен (один пакет Lemmary) | **3,9 с** | 15,0 с |
| Один вход на 3,2 тыс. токенов | **1,2 с** | 3,0 с |

Проверка качества: сходство английского и немецкого 0,917, английского и русского
0,776, несвязанного текста 0,289; векторы 1024-мерные с единичной нормой.

**OCR (docling + ocrmac)** на двухстраничной фотографии немецкого письма, снятой на
телефон (JPEG 2402×3586 и 1601×2254):

- Страница 1: 8,1 с; страница 2: 4,1 с; весь двухстраничный PDF: 14,1 с.
- Первая страница с непрогретыми моделями: 38 с.
- Все суммы совпали с исходником. Текст получился чище, чем текстовый слой
  Tesseract (OCRmyPDF) для того же PDF.

## Эксплуатация {#operations}

```sh
# status
launchctl print gui/$(id -u)/com.example.llama-embeddings | grep -E 'state|pid'
launchctl print gui/$(id -u)/com.example.docling-serve   | grep -E 'state|pid'

# restart
launchctl kickstart -k gui/$(id -u)/com.example.llama-embeddings
launchctl kickstart -k gui/$(id -u)/com.example.docling-serve

# stop / remove
launchctl bootout gui/$(id -u)/com.example.llama-embeddings
launchctl bootout gui/$(id -u)/com.example.docling-serve

# logs
tail -f ~/ai/logs/llama-embeddings.log ~/ai/logs/docling-serve.log

# OCR a file the way Lemmary does
curl -s \
  -F to_formats=md -F do_ocr=true -F image_export_mode=placeholder \
  -F "files=@some.pdf;type=application/pdf" \
  localhost:5001/v1/convert/file | python3 -c 'import json,sys; print(json.load(sys.stdin)["document"]["md_content"])'
```

**Обновление**

- llama.cpp: `brew upgrade llama.cpp`, затем перезапустите агент эмбеддингов.
- docling-serve: закреплён на версии 1.35.0. Чтобы обновить, выполните
  `uv tool install --python 3.12 docling-serve==<new> --with ocrmac --force`,
  перезапустите агент и проведите одну тестовую конвертацию. Lemmary отправляет
  `ocr_engine` только тогда, когда движок привязан, поэтому обновления образа не
  оборачиваются ошибками 422.

## Устранение неполадок {#troubleshooting}

- **Lemmary сообщает `connection refused`:** проверьте статус агента (см. выше) и
  то, вошёл ли `<USER>` в систему (`who` показывает `console`).
- **Первый OCR-документ после перезапуска завершается по таймауту:** модели ещё
  загружались. Дождитесь, пока `/ready` вернёт 200, или увеличьте
  `OCR_TIMEOUT_SEC`.
- **Эмбеддинги не строятся для очень длинных входов:** `-ub` меньше входа.
  Держите его не ниже 6144; 8192 — максимум модели.
- **Память:** вместе сервисы используют около 8,5 ГБ. На Mac с 16 ГБ и другими
  тяжёлыми нагрузками снижение `-ub` до 6144 уменьшает вычислительные буферы
  llama.cpp; на Mac с 8 ГБ запускайте только один из двух сервисов.
- **Неверный IP:** адрес Wi-Fi может меняться. Направьте Lemmary на проводной адрес
  или закрепите за Mac адрес через резервирование DHCP.

## Что делать, если OCR от Apple недостаточно хорош {#upgrade-path-if-apple-s-ocr-is-not-good-enough}

Для сложных сканов, рукописного текста или сложных таблиц на Apple Silicon хорошо
работает визуально-языковая OCR-модель (GLM-OCR, PaddleOCR-VL) через `mlx-vlm`
(`mlx_vlm.server`, OpenAI-совместимый). Lemmary обращался бы к ней через свой
OCR SDK `openai`. Этот SDK отправляет PDF как необработанные файловые части, которые
локальные VLM-серверы отклоняют, поэтому нужен небольшой адаптер, который сначала
превращает страницы в изображения. Рассчитывайте на десятки секунд на страницу, а не
на несколько секунд.
