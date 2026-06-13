# Build & distribution

This app uses **Fyne**. It builds for **macOS, Linux and Windows**.

> **protoc больше не нужен.** Раньше для Windows вшивался бинарь `protoc`;
> теперь `.proto` компилируется в процессе через `bufbuild/protocompile`
> (Go-библиотека). Ничего ставить/вшивать/скачивать не требуется ни на одной ОС.

## CI (рекомендуется)

Сборка под все платформы автоматизирована в GitHub Actions —
`.github/workflows/build.yml` (матрица `ubuntu-latest` / `macos-latest` /
`windows-latest`). Каждый раннер:

1. ставит Go (версия из `go.mod`) и Fyne CLI;
2. (Linux) ставит GUI-зависимости: `libgl1-mesa-dev xorg-dev libxcursor-dev
   libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev`;
3. `go build ./...` + `go test ./internal/...`;
4. `fyne package --os <linux|darwin|windows>`;
5. выкладывает артефакт (`.tar.xz` / `.app`→zip / `.exe`).

Запуск: вкладка **Actions → build → Run workflow** (или push в `main`).
Готовые сборки скачиваются из артефактов джоба.

## Локальная сборка

Поставить Fyne CLI:

```
go install fyne.io/tools/cmd/fyne@latest
```

Собрать пакет под текущую ОС:

```
fyne package --os darwin  --source-dir ./cmd/app --name "Proto Viewer" --app-id com.i4erkasov.proto-viewer --icon assets/icon.png
fyne package --os linux   --source-dir ./cmd/app --name "Proto Viewer" --app-id com.i4erkasov.proto-viewer --icon assets/icon.png
fyne package --os windows --source-dir ./cmd/app --name "Proto Viewer" --app-id com.i4erkasov.proto-viewer --icon assets/icon.png
```

Кросс-компиляция Fyne между ОС болезненна (GUI-тулчейн, OpenGL, CGO) —
собирай **на целевой ОС** (или в соответствующем раннере CI).

### Linux: зависимости для сборки
Нужны dev-пакеты OpenGL/X11 (см. список выше). Для запуска у пользователя —
обычные GL-драйверы (как правило уже есть).

### macOS: подпись
`.app` из CI **не подписан** → Gatekeeper покажет предупреждение
(правый клик → Open, или System Settings → Privacy & Security → Open Anyway).
Подпись/нотаризация требуют Apple Developer аккаунта — отдельный шаг при необходимости.