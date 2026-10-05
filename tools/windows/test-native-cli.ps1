param(
	[Parameter(Mandatory = $true)]
	[string] $Executable
)

$ErrorActionPreference = 'Stop'
$exe = (Resolve-Path $Executable).Path
$version = & $exe --version
if ($LASTEXITCODE -ne 0 -or $version -notmatch '^kame \S+') {
	throw "Native kame.exe version check failed: exit=$LASTEXITCODE output=$version"
}

$project = Join-Path $env:TEMP ("kame-native-windows-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $project | Out-Null
$shell = (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe').Replace('\', '/')
try {
	@'
include ./native-child.kmk

task native-windows :
	Write-Output 'native-windows-recipe-ok'
	Set-Content -Path native-env.txt -Value $env:KAME_NATIVE_ENV

task native-cwd :
	(Get-Location).Path | Set-Content -NoNewline native-cwd.txt
'@ | Set-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
	@'
task native-included :
	Write-Output 'native-include-ok'
'@ | Set-Content -Encoding ascii (Join-Path $project 'native-child.kmk')
	$pipeline = @'
task native-pipeline :
	@(out (pipe (run "__SHELL__" "-NoProfile" "-NonInteractive" "-Command" "$b=New-Object byte[] 2097152; [Console]::OpenStandardOutput().Write($b,0,$b.Length)") (run "__SHELL__" "-NoProfile" "-NonInteractive" "-Command" "$s=[Console]::OpenStandardInput(); $n=0; $b=New-Object byte[] 8192; while (($r=$s.Read($b,0,$b.Length)) -gt 0) { $n += $r }; [Console]::Write($n)")))
'@
	$pipeline.Replace('__SHELL__', $shell) | Add-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
	@'
task native-timeout :
	Set-Content recipe-pid $PID
	Write-Output "native-recipe-pid=$PID"
	Start-Process -FilePath cmd.exe -ArgumentList '/d /s /c "echo started > child-started & timeout /t 18 /nobreak >nul & echo late > descendant-marker"' -WorkingDirectory (Get-Location)
	for ($attempt = 0; $attempt -lt 40 -and !(Test-Path child-started); $attempt++) { Start-Sleep -Milliseconds 100 }
	if (!(Test-Path child-started)) { throw 'native timeout child did not start' }
	Set-Content child-launched launched
	Start-Sleep -Seconds 30
	Set-Content parent-marker late
'@ | Add-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
	@'
task native-cache :
	Add-Content -Path cache-runs.txt -Value ran
	Write-Output 'native-cache-ok'
'@ | Add-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
	Push-Location $project
	try {
		$env:KAME_NATIVE_ENV = 'inherited'
		$output = & $exe --directory $project --env kame_native_env=passed --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-windows 2>&1
		if ($LASTEXITCODE -ne 0 -or ($output -join "`n") -notmatch 'native-windows-recipe-ok') {
			throw "Native recipe check failed: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		if ((Get-Content (Join-Path $project 'native-env.txt') -Raw).Trim() -ne 'passed') {
			throw 'Native recipe did not replace the inherited environment name case-insensitively or write its output file.'
		}
		$output = & $exe --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-cwd 2>&1
		if ($LASTEXITCODE -ne 0 -or (Get-Content (Join-Path $project 'native-cwd.txt') -Raw) -ne $project) {
			throw "Native CLI working-directory selection failed: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		$output = & $exe --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-included 2>&1
		if ($LASTEXITCODE -ne 0 -or ($output -join "`n") -notmatch 'native-include-ok') {
			throw "Native source include check failed: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		$output = & $exe --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-pipeline 2>&1
		if ($LASTEXITCODE -ne 0 -or ($output -join "`n") -notmatch '2097152') {
			throw "Native binary pipeline failed to transfer 2 MiB through both stages: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		$cacheArgs = @('--json', '--directory', $project, '--shell', $shell, '--shell', '-NoProfile', '--shell', '-NonInteractive', '--shell', '-Command', '-f', 'Makefile.kmk', 'native-cache')
		$null = & $exe @cacheArgs 2>&1
		if ($LASTEXITCODE -ne 0 -or !(Test-Path (Join-Path $project 'cache-runs.txt'))) {
			throw "Native cache miss failed: exit=$LASTEXITCODE"
		}
		$cacheOutput = & $exe @cacheArgs 2>&1
		$cacheRuns = @(Get-Content (Join-Path $project 'cache-runs.txt'))
		if ($LASTEXITCODE -ne 0 -or $cacheRuns.Count -ne 1 -or ($cacheOutput -join "`n") -notmatch '"cached":true') {
			throw "Native cache hit failed: exit=$LASTEXITCODE runs=$($cacheRuns.Count) output=$($cacheOutput -join ' | ')"
		}
		$timeoutOutput = & $exe --timeout 15000 --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-timeout 2>&1
		$timeoutStatus = $LASTEXITCODE
		Start-Sleep -Seconds 20
		Write-Output ($timeoutOutput -join "`n")
		$childStarted = Test-Path (Join-Path $project 'child-started')
		$childLaunched = Test-Path (Join-Path $project 'child-launched')
		$descendantFinished = Test-Path (Join-Path $project 'descendant-marker')
		$parentFinished = Test-Path (Join-Path $project 'parent-marker')
		$recipePid = if (Test-Path (Join-Path $project 'recipe-pid')) { (Get-Content -Raw (Join-Path $project 'recipe-pid')).Trim() } else { 'missing' }
		if ($timeoutStatus -eq 0 -or ($timeoutOutput -join "`n") -notmatch 'RECIPE_TIMEOUT' -or $recipePid -eq 'missing' -or !$childStarted -or !$childLaunched -or $descendantFinished -or $parentFinished) {
			throw "Native timeout did not stop and reap its process tree: exit=$timeoutStatus recipePid=$recipePid childStarted=$childStarted childLaunched=$childLaunched descendantFinished=$descendantFinished parentFinished=$parentFinished output=$($timeoutOutput -join ' | ')"
		}
	} finally {
		Pop-Location
	}
} finally {
	Remove-Item -Recurse -Force $project
}

$global:LASTEXITCODE = 0
Write-Output "Native Windows CLI passed: $version"
