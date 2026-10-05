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
try {
	@'
task native-windows :
	Write-Output 'native-windows-recipe-ok'
	Set-Content -Path native-env.txt -Value $env:KAME_NATIVE_ENV
'@ | Set-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
	@'
task native-timeout :
	$childScript = "Set-Content child-started started; Start-Sleep -Seconds 18; Set-Content descendant-marker late"
	$encoded = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($childScript))
	Start-Process -FilePath powershell.exe -ArgumentList "-NoProfile -NonInteractive -EncodedCommand $encoded" -WorkingDirectory (Get-Location)
	Start-Sleep -Seconds 30
	Set-Content parent-marker late
'@ | Add-Content -Encoding ascii (Join-Path $project 'Makefile.kmk')
	$shell = (Join-Path $env:SystemRoot 'System32/WindowsPowerShell/v1.0/powershell.exe').Replace('\', '/')
	Push-Location $project
	try {
		$output = & $exe --env KAME_NATIVE_ENV=passed --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-windows 2>&1
		if ($LASTEXITCODE -ne 0 -or ($output -join "`n") -notmatch 'native-windows-recipe-ok') {
			throw "Native recipe check failed: exit=$LASTEXITCODE output=$($output -join ' | ')"
		}
		if ((Get-Content (Join-Path $project 'native-env.txt') -Raw).Trim() -ne 'passed') {
			throw 'Native recipe did not receive the CLI environment override or write its output file.'
		}
		$timeoutOutput = & $exe --timeout 15000 --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-timeout 2>&1
		$timeoutStatus = $LASTEXITCODE
		Start-Sleep -Seconds 20
		if ($timeoutStatus -eq 0 -or ($timeoutOutput -join "`n") -notmatch 'RECIPE_TIMEOUT' -or !(Test-Path (Join-Path $project 'child-started')) -or (Test-Path (Join-Path $project 'descendant-marker')) -or (Test-Path (Join-Path $project 'parent-marker'))) {
			throw "Native timeout did not stop and reap its process tree: exit=$timeoutStatus output=$($timeoutOutput -join ' | ')"
		}
	} finally {
		Pop-Location
	}
} finally {
	Remove-Item -Recurse -Force $project
}

$global:LASTEXITCODE = 0
Write-Output "Native Windows CLI passed: $version"
