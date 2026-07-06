//go:build windows

package main

import _ "embed"

//go:embed assets/xray/xray.exe
var xrayEXE []byte

//go:embed assets/xray/geoip.dat
var geoipDAT []byte

//go:embed assets/xray/geosite.dat
var geositeDAT []byte

//go:embed assets/xray/wintun.dll
var wintunXrayDLL []byte
