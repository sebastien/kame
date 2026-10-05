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
	Start-Sleep -Seconds 30
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
		$timeoutOutput = & $exe --timeout 5000 --shell $shell --shell -NoProfile --shell -NonInteractive --shell -Command -f Makefile.kmk native-timeout 2>&1
		$timeoutStatus = $LASTEXITCODE
		if ($timeoutStatus -eq 0 -or ($timeoutOutput -join "`n") -notmatch 'RECIPE_TIMEOUT') {
			throw "Native timeout check failed: exit=$timeoutStatus output=$($timeoutOutput -join ' | ')"
		}
	} finally {
		Pop-Location
	}
} finally {
	Remove-Item -Recurse -Force $project
}

Write-Output "Native Windows CLI passed: $version"
