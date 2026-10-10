# Сборка и выпуск

## Linux VPS

Установите Docker Engine из официального репозитория для вашего дистрибутива, с проверкой подписей пакетов. Проверены Engine/CLI 28.4.0, BuildKit через встроенный docker driver. Не требуется устанавливать Go на VPS. Нужны исходящий HTTPS к Docker Hub для первого скачивания образа и GitHub для исходников. После загрузки образа Go-модули из сети не скачиваются.

```bash
git clone https://github.com/andluk87/lanprint.git
cd lanprint
bash build.sh
```

Скрипт выполняет `docker build --target artifact --output type=local,dest=. .`. BuildKit автоматически экспортирует только EXE и его SHA-256 в текущий корень проекта. Финальный scratch-stage не содержит исходников/временных файлов. Компиляция выполняется в Linux-контейнере с `GOOS=windows GOARCH=amd64 CGO_ENABLED=0`.

Go: 1.24.7; базовый Debian bookworm image: `golang:1.24.7-bookworm@sha256:b8bae5bd9ba9b1f89b635c91c24cc75cea335a16fb5076310f38566fc674b1ec`. Внешних библиотек нет. `-trimpath`, `-buildvcs=false`, пустой buildid исключают путь сборки и Git metadata из бинарника. Изменение исходников версии меняет результат. Контрольная сумма выпуска — отдельный файл, EXE в Git не хранится.

Проверка повторяемости:

```bash
cp LANPrint.exe.sha256 /tmp/lanprint-first.sha256
bash build.sh
cmp /tmp/lanprint-first.sha256 LANPrint.exe.sha256
```

Docker cache повторно использует неизменённые этапы. Для независимой сборки: `docker build --no-cache --target artifact --output type=local,dest=. .`; сравните SHA-256. Docker автоматически выбирает параллелизм Go по доступным CPU; нет необходимости запускать несколько копий сборки.

Из Windows с Docker Desktop (Linux containers): `powershell -NoProfile -File .\build.ps1`. Но выбранный основной workflow — Linux VPS. Альтернатива с Compose v2: `docker compose run --build --rm build`, экспортируется та же пара файлов.

## Тесты только внутри Docker

Основная Docker-сборка обязательно выполняет `CGO_ENABLED=1 go test -race -count=1 -v ./...`; CGO нужен только Linux race detector и выключен для Windows EXE. Ошибка тестов или компиляции прерывает сборку.

Дополнительный аудит всех пакетов и короткий fuzz-run выполняйте в образе builder:

```bash
docker build --target build -t lanprint-builder:0.1.1 .
docker run --rm -e CGO_ENABLED=1 lanprint-builder:0.1.1 go test -race ./...
docker run --rm -e CGO_ENABLED=1 lanprint-builder:0.1.1 go test ./internal/core -run '^$' -fuzz FuzzDecodeIPP -fuzztime 10s -parallel 2
docker run --rm -e GOOS=windows -e GOARCH=amd64 lanprint-builder:0.1.1 go vet ./...
```

## GitHub

Push/PR запускает Docker-сборку, EXE и SHA-256 сохраняются как Actions artifact. Push тега `v0.1.1` запускает release job:

```bash
git tag v0.1.1
git push origin v0.1.1
```

Workflow проверяет соответствие тега `core.Version`, затем публикует Release через предоставляемый Actions `github.token`. Права `contents: write` выдаются только release job. Все используемые Actions зафиксированы по commit SHA. Для следующего выпуска измените `core.Version`, версии в документации/Compose и выпустите соответствующий семантический тег. Публикуйте версию только после проверки Windows/оборудования или явно маркируйте её как предварительную.

Наличие workflow не означает, что GitHub Actions уже выполнился. Проверяйте статус run и фактические release assets; права репозитория/Actions могут требовать настройки владельцем. EXE не подписан Authenticode, SHA-256 подтверждает совпадение скачанного файла, но не удостоверяет издателя.
