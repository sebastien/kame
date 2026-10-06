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

task native-path :
	@(out (run "cmd.exe" "/c" "echo native-path-ok"))
'@ | Set-Content -Encoding ascii (Join-Path $project 'native-child.kmk')
	@'
task native-pipeline :
	@(out (pipe (run "powershell.exe" "-NoProfile" "-NonInteractive" "-Command" "Set-Content pipeline-stage-one-started yes; [Console]::Error.Write('native-pipeline-stderr'); $b=New-Object byte[] 65536; $s=[Console]::OpenStandardOutput(); for ($i=1; $i -le 32; $i++) { $s.Write($b,0,$b.Length); Set-Content pipeline-stage-one-bytes ($i * $b.Length) }; Set-Content pipeline-stage-one-done yes") (run "powershell.exe" "-NoProfile" "-NonInteractive" "-Command" "Set-Content pipeline-stage-two-started yes; $s=[Console]::OpenStandardInput(); $n=0; $b=New-Object byte[] 8192; while (($r=$s.Read($b,0,$b.Length)) -gt 0) { $n += $r; if (($n % 262144) -lt $r) { Set-Content pipeline-stage-two-bytes $n } }; Set-Content pipeline-stage-two-done yes; [Console]::Write($n)")))
'@ | Add-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
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
task native-cache : ./cache-input.txt
	Add-Content -Path cache-runs.txt -Value ran
	Write-Output 'native-cache-ok'

task native-concurrent-cache : ./cache-input.txt
	Add-Content -Path concurrent-runs.txt -Value ran
	Set-Content concurrent-first-started yes
	Start-Sleep -Seconds 5
	Write-Output 'native-concurrent-cache-ok'

./native-watch-output.txt : ./native-watch-input.txt
	Copy-Item -LiteralPath @< -Destination @>

./native-watch-glob-output.txt : ./native-watch-inputs/*.txt
	Add-Content -Path native-watch-glob-runs.txt -Value ran
	Set-Content -Path @> -Value done
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
		$readOutput = & $exe do run --lang expr --allow-read -C $project -c '(read "native-env.txt")' 2>&1
		if ($LASTEXITCODE -ne 0 -or ($readOutput -join "`n") -notmatch 'passed') {
			throw "Native file read operation failed: exit=$LASTEXITCODE output=$($readOutput -join ' | ')"
		}
		$existsOutput = & $exe do run --lang expr --allow-read -C $project -c '(exists? "native-env.txt")' 2>&1
		if ($LASTEXITCODE -ne 0 -or ($existsOutput -join "`n") -notmatch ':true') {
			throw "Native file existence operation failed: exit=$LASTEXITCODE output=$($existsOutput -join ' | ')"
		}
		$statOutput = & $exe do run --lang expr --allow-read -C $project -c '(stat "native-env.txt")' 2>&1
		if ($LASTEXITCODE -ne 0 -or ($statOutput -join "`n") -notmatch 'name: "native-env.txt"') {
			throw "Native file stat operation failed: exit=$LASTEXITCODE output=$($statOutput -join ' | ')"
		}
		$wildcardOutput = & $exe do run --lang expr --allow-read -C $project -c '(wildcard "*.kmk")' 2>&1
		if ($LASTEXITCODE -ne 0 -or ($wildcardOutput -join "`n") -notmatch 'native-child.kmk' -or ($wildcardOutput -join "`n") -notmatch 'Makefile.kmk') {
			throw "Native wildcard operation failed: exit=$LASTEXITCODE output=$($wildcardOutput -join ' | ')"
		}
		Push-Location $env:TEMP
		try {
			$output = & $exe --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-cwd 2>&1
			$cwdStatus = $LASTEXITCODE
		} finally {
			Pop-Location
		}
		$actualCwd = if (Test-Path (Join-Path $project 'native-cwd.txt')) { (Get-Content (Join-Path $project 'native-cwd.txt') -Raw).TrimEnd([char[]]@('\', '/')) } else { '' }
		$actualCwdLeaf = if ($actualCwd) { $actualCwd.Split([char[]]@('\', '/'))[-1] } else { '' }
		if ($cwdStatus -ne 0 -or $actualCwdLeaf -ne (Split-Path -Leaf $project)) {
			throw "Native CLI working-directory selection failed: exit=$cwdStatus actual=$actualCwd output=$($output -join ' | ')"
		}
		$output = & $exe --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-included 2>&1
		if ($LASTEXITCODE -ne 0 -or ($output -join "`n") -notmatch 'native-include-ok') {
			throw "Native source include check failed: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		$output = & $exe --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-path 2>&1
		if ($LASTEXITCODE -ne 0 -or ($output -join "`n") -notmatch 'native-path-ok') {
			throw "Native PATH executable lookup failed: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		$pipelineOutputPath = Join-Path $project 'pipeline.out'
		$pipelineErrorPath = Join-Path $project 'pipeline.err'
		$null = & $exe --timeout 30000 --directory $project --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-pipeline 1> $pipelineOutputPath 2> $pipelineErrorPath
		$pipelineStatus = $LASTEXITCODE
		$pipelineOutput = Get-Content -Raw $pipelineOutputPath
		$pipelineError = Get-Content -Raw $pipelineErrorPath
		if ($pipelineStatus -ne 0 -or $pipelineOutput -notmatch '2097152' -or $pipelineOutput -notmatch '"stages":\[\{[^}]*"status":0\},\{[^}]*"status":0\}\]' -or $pipelineError -notmatch 'native-pipeline-stderr') {
			$stageMarkers = @('pipeline-stage-one-started', 'pipeline-stage-one-done', 'pipeline-stage-two-started', 'pipeline-stage-two-done') | ForEach-Object { "$_=$(Test-Path (Join-Path $project $_))" }
			$stageProgress = @('pipeline-stage-one-bytes', 'pipeline-stage-two-bytes') | ForEach-Object { "$_=$(if (Test-Path (Join-Path $project $_)) { (Get-Content -Raw (Join-Path $project $_)).Trim() } else { '0' })" }
			$pipelineFailure = "Native binary pipeline failed to preserve 2 MiB flow, per-stage status, or stream separation: exit=$pipelineStatus markers=$($stageMarkers -join ',') progress=$($stageProgress -join ',') stdout=$pipelineOutput stderr=$pipelineError"
		}
	Set-Content -Path (Join-Path $project 'cache-input.txt') -Value 'initial'
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
		Set-Content -Path (Join-Path $project 'cache-input.txt') -Value 'changed'
		$cacheOutput = & $exe @cacheArgs 2>&1
		$cacheRuns = @(Get-Content (Join-Path $project 'cache-runs.txt'))
		if ($LASTEXITCODE -ne 0 -or $cacheRuns.Count -ne 2 -or ($cacheOutput -join "`n") -match '"cached":true') {
			throw "Native cache input invalidation failed: exit=$LASTEXITCODE runs=$($cacheRuns.Count) output=$($cacheOutput -join ' | ')"
		}
		$concurrentArgs = @('--json', '--directory', $project, '--shell', $shell, '--shell', '-NoProfile', '--shell', '-NonInteractive', '--shell', '-Command', '-f', 'Makefile.kmk', 'native-concurrent-cache')
		$firstOutput = Join-Path $project 'concurrent-1.out'
		$firstError = Join-Path $project 'concurrent-1.err'
		$secondOutput = Join-Path $project 'concurrent-2.out'
		$secondError = Join-Path $project 'concurrent-2.err'
		$firstRun = Start-Process -FilePath $exe -ArgumentList $concurrentArgs -PassThru -NoNewWindow -RedirectStandardOutput $firstOutput -RedirectStandardError $firstError
		$lockDeadline = [DateTime]::UtcNow.AddSeconds(15)
		while ([DateTime]::UtcNow -lt $lockDeadline -and !(Test-Path (Join-Path $project 'concurrent-first-started'))) {
			if ($firstRun.HasExited) { throw "Native first concurrent cache recipe exited before its lock handshake: code=$($firstRun.ExitCode) stdout=$(Get-Content -Raw $firstOutput) stderr=$(Get-Content -Raw $firstError)" }
			Start-Sleep -Milliseconds 50
		}
		if (!(Test-Path (Join-Path $project 'concurrent-first-started'))) {
			Stop-Process -Id $firstRun.Id -Force -ErrorAction SilentlyContinue
			throw 'Native first concurrent cache recipe did not enter before the lock handshake deadline.'
		}
		$secondRun = Start-Process -FilePath $exe -ArgumentList $concurrentArgs -PassThru -NoNewWindow -RedirectStandardOutput $secondOutput -RedirectStandardError $secondError
		if (!$firstRun.WaitForExit(30000) -or !$secondRun.WaitForExit(30000)) {
			Stop-Process -Id $firstRun.Id, $secondRun.Id -Force -ErrorAction SilentlyContinue
			throw 'Native concurrent cache miss did not finish within 30 seconds.'
		}
		if ($firstRun.ExitCode -ne 0 -or $secondRun.ExitCode -ne 0) {
			throw "Native concurrent cache commands failed: exits=$($firstRun.ExitCode),$($secondRun.ExitCode) errors=$(Get-Content -Raw $firstError),$(Get-Content -Raw $secondError)"
		}
		$concurrentRuns = @(Get-Content (Join-Path $project 'concurrent-runs.txt'))
		if ($concurrentRuns.Count -ne 1) {
			throw "Native concurrent cache misses were not serialized: runs=$($concurrentRuns.Count) first=stdout:$(Get-Content -Raw $firstOutput) stderr:$(Get-Content -Raw $firstError) second=stdout:$(Get-Content -Raw $secondOutput) stderr:$(Get-Content -Raw $secondError)"
		}
		Set-Content -Path (Join-Path $project 'native-watch-input.txt') -Value 'watch-before'
		New-Item -ItemType Directory -Path (Join-Path $project 'native-watch-inputs') | Out-Null
		Set-Content -Path (Join-Path $project 'native-watch-inputs/a.txt') -Value a
		$watchArgs = @('--directory', $project, '--shell', $shell, '--shell', '-NoProfile', '--shell', '-NonInteractive', '--shell', '-Command', '--watch', './native-watch-output.txt', './native-watch-glob-output.txt')
		$watchStdout = Join-Path $project 'watch.out'
		$watchStderr = Join-Path $project 'watch.err'
		$watch = Start-Process -FilePath $exe -ArgumentList $watchArgs -PassThru -NoNewWindow -RedirectStandardOutput $watchStdout -RedirectStandardError $watchStderr
		try {
			$watchOutput = Join-Path $project 'native-watch-output.txt'
			$globOutput = Join-Path $project 'native-watch-glob-output.txt'
			$deadline = [DateTime]::UtcNow.AddSeconds(15)
			while ([DateTime]::UtcNow -lt $deadline -and (!(Test-Path $watchOutput) -or (Get-Content -Raw $watchOutput).Trim() -ne 'watch-before' -or !(Test-Path $globOutput))) {
				if ($watch.HasExited) { throw "Native watch exited before its initial build: code=$($watch.ExitCode) stderr=$(Get-Content -Raw $watchStderr)" }
				Start-Sleep -Milliseconds 100
			}
			if (!(Test-Path $watchOutput) -or (Get-Content -Raw $watchOutput).Trim() -ne 'watch-before' -or !(Test-Path $globOutput)) {
				throw "Native watch did not publish its initial output: stderr=$(Get-Content -Raw $watchStderr)"
			}
			Set-Content -Path (Join-Path $project 'native-watch-input.txt') -Value 'watch-after'
			$deadline = [DateTime]::UtcNow.AddSeconds(15)
			while ([DateTime]::UtcNow -lt $deadline -and (Get-Content -Raw $watchOutput).Trim() -ne 'watch-after') {
				if ($watch.HasExited) { throw "Native watch exited before input invalidation: code=$($watch.ExitCode) stderr=$(Get-Content -Raw $watchStderr)" }
				Start-Sleep -Milliseconds 100
			}
			if ((Get-Content -Raw $watchOutput).Trim() -ne 'watch-after') {
				throw "Native watch did not rebuild after input invalidation: stderr=$(Get-Content -Raw $watchStderr)"
			}
			Set-Content -Path (Join-Path $project 'native-watch-inputs/b.txt') -Value b
			$deadline = [DateTime]::UtcNow.AddSeconds(15)
			while ([DateTime]::UtcNow -lt $deadline -and @(Get-Content (Join-Path $project 'native-watch-glob-runs.txt')).Count -lt 2) {
				if ($watch.HasExited) { throw "Native watch exited before glob addition: code=$($watch.ExitCode) stderr=$(Get-Content -Raw $watchStderr)" }
				Start-Sleep -Milliseconds 100
			}
			$globRuns = @(Get-Content (Join-Path $project 'native-watch-glob-runs.txt'))
			if ($globRuns.Count -ne 2) { throw "Native watch did not rebuild after glob membership addition: runs=$($globRuns.Count) stderr=$(Get-Content -Raw $watchStderr)" }
			Remove-Item (Join-Path $project 'native-watch-inputs/b.txt')
			$deadline = [DateTime]::UtcNow.AddSeconds(15)
			while ([DateTime]::UtcNow -lt $deadline -and @(Get-Content (Join-Path $project 'native-watch-glob-runs.txt')).Count -lt 3) {
				if ($watch.HasExited) { throw "Native watch exited before glob removal: code=$($watch.ExitCode) stderr=$(Get-Content -Raw $watchStderr)" }
				Start-Sleep -Milliseconds 100
			}
			$globRuns = @(Get-Content (Join-Path $project 'native-watch-glob-runs.txt'))
			if ($globRuns.Count -ne 3) { throw "Native watch did not rebuild after glob membership removal: runs=$($globRuns.Count) stderr=$(Get-Content -Raw $watchStderr)" }
		} finally {
			if (!$watch.HasExited) { Stop-Process -Id $watch.Id -Force }
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
		if ($pipelineFailure) { throw $pipelineFailure }
	} finally {
		Pop-Location
	}
} finally {
	Remove-Item -Recurse -Force $project
}

$global:LASTEXITCODE = 0
Write-Output "Native Windows CLI passed: $version"
