# Windows build (installer + embedded protoc)

This app uses **Fyne**.

## Goal: share with non-developers

You have 2 practical delivery formats:

### 1) Portable build (recommended for internal sharing)

Give colleagues a single folder or zip:

- `Proto Viewer.exe`
- (optionally) a `README.txt`

They unzip and run `Proto Viewer.exe`.

### 2) Installer (recommended for wider distribution)

Generate an installer (MSI/EXE). Colleagues run the installer and then launch the app from Start Menu.

---

## What about `protoc`?

- **Windows**: `protoc` is embedded in the app build as `internal/infrastructure/protocbin/protoc_windows_amd64.zip`.
  On first use it’s unpacked to:

  `%LOCALAPPDATA%\proto-viewer\protoc\<hash>\bin\protoc.exe`

  and used automatically (no admin rights needed).

- **macOS/Linux**: we currently rely on `protoc` being available in `PATH` (keeps repo size smaller).

> Important: the repository currently contains an **empty placeholder** zip. You must replace it with a real protoc zip.

## Provide protoc zip (Windows)

1. Download the official protoc Windows archive from Google:
   `protoc-<version>-win64.zip`
2. Copy it into:
   `internal/infrastructure/protocbin/protoc_windows_amd64.zip`

No code changes needed.

---

## Build Windows `.exe` (build on Windows)

Cross-compiling Fyne apps from macOS to Windows is often painful (GUI toolchain, OpenGL, CGO).
The recommended way is to build **on Windows** (or in a Windows CI runner).

### Prerequisites on Windows

- Go
- A C compiler required by Fyne (MSYS2 / mingw-w64 is the common choice)
- Fyne CLI:

  `go install fyne.io/fyne/v2/cmd/fyne@latest`

### Build executable

From repo root:

- `fyne package -os windows -icon assets/icon.png -name "Proto Viewer" -appID com.i4erkasov.proto-viewer`

This produces a Windows executable (name/output location depends on Fyne version).

---

## Build an installer

### Option A: Fyne packaging (simplest)

Run on Windows:

- `fyne package -os windows -name "Proto Viewer" -appID com.i4erkasov.proto-viewer -icon assets/icon.png`

Depending on your environment/tooling, Fyne can output an installer (MSI/EXE) or a packaged app.

### Option B: WiX / Inno Setup (most control)

If you need a custom install path, extra files, shortcuts etc:

- **WiX Toolset** (MSI)
- **Inno Setup** (EXE)

In both cases you ship `Proto Viewer.exe` + assets (if any). `protoc` is already embedded and will be unpacked automatically at runtime.

---

## CI option (recommended)

If you don’t want to build on your local Windows machine:

- Use a Windows GitHub Actions runner
- Build with `fyne package -os windows ...`
- Upload resulting `.exe` and/or installer as workflow artifacts

That way you can send colleagues a download link to a ready-to-run build.
