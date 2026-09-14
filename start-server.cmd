@echo off
setlocal
chcp 65001 >nul
pushd "%~dp0"
set "GOFLAGS=%GOFLAGS% -buildvcs=false"
if exist "go.mod" (
  go run ./cmd/ptcgp-launcher %*
) else if exist "bin\ptcgp-launcher.exe" (
  "bin\ptcgp-launcher.exe" %*
) else (
  echo Installation incomplete : bin\ptcgp-launcher.exe est absent. 1>&2
  exit /b 1
)
set "result=%errorlevel%"
popd
exit /b %result%
