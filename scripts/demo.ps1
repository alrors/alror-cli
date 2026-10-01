<#
.SYNOPSIS
  Guided, interactive tour of the alror CLI.

.DESCRIPTION
  Builds the CLI, creates a sandbox git repository with a realistic
  AI-authored change, then walks through every command. Each step explains
  what is about to happen, shows the command and runs it live in your
  terminal, so you see the real colours, spinners, traffic sweeps and
  release receipts.

  Press Enter to run each step, S to skip it, Q to quit.

.EXAMPLE
  .\scripts\demo.ps1
.EXAMPLE
  .\scripts\demo.ps1 -Auto        # no pauses, run straight through
#>
[CmdletBinding()]
param(
  [switch]$Auto,      # don't pause between steps
  [switch]$NoBuild,   # use the existing bin\alror.exe
  [switch]$KeepRepo   # keep the sandbox repository afterwards
)

$ErrorActionPreference = 'Continue'
$root = Split-Path -Parent $PSScriptRoot
$alror = Join-Path $root 'bin\alror.exe'
$step = 0

function Say([string]$text, [string]$color = 'Gray') { Write-Host $text -ForegroundColor $color }

function Step([string]$title, [string]$explain, [string[]]$cmdArgs) {
  $script:step++
  Write-Host ''
  Write-Host ('─' * 72) -ForegroundColor DarkGray
  Write-Host (" {0,2}. {1}" -f $script:step, $title) -ForegroundColor White
  foreach ($line in ($explain -split "`n")) { Say "     $line" 'DarkGray' }
  Write-Host ''
  Write-Host '   $ ' -NoNewline -ForegroundColor Green
  Write-Host ('alror ' + ($cmdArgs -join ' ')) -ForegroundColor White
  if (-not $Auto) {
    Write-Host '   [Enter] run   [S] skip   [Q] quit ' -NoNewline -ForegroundColor DarkGray
    $key = [Console]::ReadKey($true).Key
    Write-Host ''
    if ($key -eq 'Q') { throw 'quit' }
    if ($key -eq 'S') { Say '   skipped' 'DarkGray'; return $null }
  }
  Write-Host ''
  Push-Location $sandbox
  try { & $alror @cmdArgs } finally { Pop-Location }
  $code = $LASTEXITCODE
  $meaning = switch ($code) { 0 { 'ok' } 1 { 'error' } 2 { 'rolled back' } 3 { 'risk gate tripped' } default { '' } }
  Say ("   exit code {0} ({1})" -f $code, $meaning) 'DarkGray'
  return $code
}

# ------------------------------------------------------------------ setup
Clear-Host
Say "`n  ALROR CLI TOUR" 'White'
Say '  Risk-scored, progressively rolled out, verified, auto-reversed releases.' 'DarkGray'
Say "  Everything runs locally: simulated traffic and metrics, no cloud, no cluster.`n" 'DarkGray'

if (-not $NoBuild) {
  Say '  Building the CLI…' 'DarkGray'
  Push-Location $root
  go build -o bin/ ./cmd/...
  $built = $LASTEXITCODE
  Pop-Location
  if ($built -ne 0) { Say '  go build failed' 'Red'; exit 1 }
}
if (-not (Test-Path $alror)) { Say "  CLI not found at $alror" 'Red'; exit 1 }

$sandbox = Join-Path ([IO.Path]::GetTempPath()) ('alror-tour-' + (Get-Random))
New-Item -ItemType Directory -Path $sandbox | Out-Null
Push-Location $sandbox
git init -q -b main 2>$null
git config user.email 'you@example.com'
git config user.name 'You'
New-Item -ItemType Directory -Force 'services/checkout', 'web' | Out-Null
Set-Content 'services/checkout/main.go' 'package main'
Set-Content 'web/index.ts' 'export {}'
git add -A 2>$null; git commit -q -m 'initial commit' 2>$null
git checkout -q -b feature/batch-retries 2>$null
1..120 | ForEach-Object { "func retryCapture$_() {}" } | Set-Content 'services/checkout/payment.go'
Add-Content 'web/index.ts' 'export const retries = 3'
git add -A 2>$null
git commit -q -m "Batch retries for payment capture`n`nCo-Authored-By: Claude <noreply@anthropic.com>" 2>$null
Pop-Location
Say "  Sandbox repo: $sandbox" 'DarkGray'
Say '  Branch feature/batch-retries: 120 new lines in services/checkout/payment.go, a small' 'DarkGray'
Say '  change in web/, committed with a coding-agent trailer. No tests were touched.' 'DarkGray'

try {
  Step 'Meet the CLI' `
    'Running alror with no arguments shows the banner and where to start.' `
    @()

  Step 'Set up the repository' `
    "init writes alror.yaml (your services, metrics and rollback policy) and an`n.alror/ folder where every deployment and event is stored as plain JSON." `
    @('init')

  Say "`n   alror.yaml now maps services to source paths:" 'DarkGray'
  Get-Content (Join-Path $sandbox 'alror.yaml') | Select-Object -First 16 | ForEach-Object { Say "     $_" 'DarkGray' }

  Step 'Score the change' `
    "risk reads the git diff against main and explains every point: critical service,`nsensitive payment path, no tests, AI-authored. The score then picks the rollout plan." `
    @('risk', '--base', 'main')

  Step 'Ship it: a healthy release' `
    "deploy shifts traffic step by step (5% → 25% → 50%), bakes, compares canary vs baseline`nwith a statistical test, then promotes to 100% and prints a release receipt.`n(bake_scale makes each 10-minute bake take about 2 seconds in this demo.)" `
    @('deploy', '-s', 'checkout-api', '-i', 'registry/checkout:1.42', '--ref', '#4821', '--base', 'main')

  Step 'A bad release gets caught' `
    "--regress makes the simulated canary's error rate 80% worse. Watch the verifier fail`nthe 5% step and the engine roll traffic back on its own. Exit code 2 tells CI." `
    @('deploy', '-s', 'checkout-api', '-i', 'registry/checkout:1.43', '--ref', '#4822', '--regress', 'error_rate=1.8', '--risk', '55')

  Step 'Shadow mode' `
    "With --shadow, Alror only recommends: it logs that it would have rolled back,`nbut keeps going. This is how teams build trust before turning on auto-rollback." `
    @('deploy', '-s', 'web-frontend', '-i', 'registry/web:2.0', '--regress', 'latency_p95=1.6', '--shadow', '--risk', '20')

  Step 'History' `
    'status lists every release with its risk and outcome.' `
    @('status')

  $last = (& $alror -C $sandbox --json status 2>$null | ConvertFrom-Json) | Where-Object { $_.status -eq 'promoted' } | Select-Object -First 1
  if ($last) {
    Step 'One release, in detail' `
      'status <id> shows the rollout stages, the reason and the full event log. A unique id prefix is enough.' `
      @('status', $last.id)

    Step 'Roll back by hand' `
      'rollback reverses any deployment through its driver and records why in the event log.' `
      @('rollback', $last.id, '--reason', 'tour: manual rollback')
  }

  Step 'Risk remembers' `
    "Rollbacks in the last 30 days raise the score of new changes to the same service.`nRun risk again and look for the Recent rollbacks factor." `
    @('risk', '--base', 'main')

  Step 'Pull-request check' `
    "In GitHub Actions this posts an 'Alror / change-risk' check and one sticky PR comment.`nLocally, --dry-run prints the exact Markdown reviewers would see." `
    @('github', 'check', '--dry-run', '--base', 'main')

  Step 'Health check' `
    'doctor verifies git, alror.yaml, the state folder, target tools and metrics.' `
    @('doctor')

  Step 'Machine-readable output' `
    'Every command supports --json, so CI and other tools can read results.' `
    @('--json', 'status', '-n', '2')

  Write-Host ''
  Write-Host ('─' * 72) -ForegroundColor DarkGray
  Say ' Done. Next things to try:' 'White'
  Say "   alror docs --open                    full documentation at http://127.0.0.1:4100" 'Gray'
  Say "   alror -C $sandbox status" 'Gray'
  Say '   Web console: cd ..\web; npm run dev → http://localhost:3000/login (admin / alror-demo)' 'Gray'
} catch {
  if ($_.Exception.Message -ne 'quit') { throw }
  Say "`n  Tour ended." 'DarkGray'
} finally {
  if ($KeepRepo) { Say "`n  Sandbox kept at $sandbox" 'DarkGray' }
  else { Remove-Item -Recurse -Force $sandbox -ErrorAction SilentlyContinue }
}
