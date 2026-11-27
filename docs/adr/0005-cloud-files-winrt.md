# ADR-0005: Cloud Files via WinRT `StorageProviderSyncRootManager`

**Status:** Accepted
**Datum:** 2026-04-30

## Kontext

Der Desktop-Client (Tauri/Rust) registriert sich auf Windows als Cloud-
Files-Provider, damit Explorer Status-Icons, On-Demand-Hydration und
Placeholder-Dateien anzeigt.

Win32 bietet `CfRegisterSyncRoot` als direkten Aufruf. Allein reicht das
nicht: der Cloud-Filter-Kernel-Treiber sieht den Provider erst, wenn
zusätzlich Storage-Provider-Registry-Einträge unter
`HKCU\Software\Classes\CLSID\{providerId}` existieren. Ohne diese lehnt
jedes `CfConvertToPlaceholder` mit `0x8007017C` ab. Die WinRT-API
`StorageProviderSyncRootManager.Register` schreibt diese Einträge mit.

## Entscheidung

`StorageProviderSyncRootManager.Register` aus WinRT verwenden.

- Kein vorgelagerter Force-Unregister: bei On-Demand-Roots würde er
  dehydrierte Placeholder unwiederbringlich zerstören (Bytes nur in der
  Cloud, lokaler Placeholder wäre weg).
- Provider-Properties am `StorageProviderSyncRootInfo` setzen
  (`AllowPinning`, `AllowHydration`, `RecycleBinUri`, `IconResource`).
- WinRT-Aufruf gekapselt in Rust via `windows`-Crate; Cloud-Files-Modul
  bleibt Windows-only.

## Konsequenzen

**Positiv:**
- Status-Icons + Hydration funktionieren ohne Workarounds.
- Provider übersteht App-Updates ohne Datenverlust an Placeholdern.

**Negativ:**
- WinRT-Aufrufe schließen ältere Windows-Versionen aus (Win 10 1709+ ist
  Mindestvoraussetzung — pragmatisch, da Cloud Files sowieso erst ab dort
  existiert).
- Manuelles Spalten-Aktivieren im Explorer-Details-View ist Workaround, bis
  eine `privasync_shell.dll` (`IColumnProvider`) existiert.
