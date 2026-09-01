param([string]$Message = "LABOSURF PRO: mise à jour")
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$wslRoot = "/mnt/" + ($root.Substring(0,1).ToLower()) + $root.Substring(2).Replace('\','/')
wsl bash -lc "cd '$wslRoot' && ./tools/deploy.sh '$Message'"
