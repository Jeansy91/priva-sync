; NSIS install hooks for the PrivaSync Explorer context-menu shell extension
; (ADR-0080). The identity package and public certificate are bundle resources.
!include "LogicLib.nsh"

!macro PRIVASYNC_CONTEXTMENU_ACTION action
  nsExec::ExecToLog 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$INSTDIR\register-context-menu.ps1" -Action ${action}'
  Pop $0
  ${If} $0 != 0
    DetailPrint "PrivaSync Explorer context-menu registration failed ($0)."
    Abort
  ${EndIf}
!macroend

; Free the shell extension from whoever has it mapped, so the installer can
; overwrite it.
;
; Explorer on Windows 10 and the COM surrogate on Windows 11 can hold an open
; handle to the DLL for as long as the shell extension is loaded. Every upgrade
; can therefore hit "Error opening file for writing" on privasync_shell.dll.
;
; A loaded module cannot be deleted, but it can be renamed. So: unregister
; first, so the shell stops handing out new instances; then move the old file
; aside and have Windows delete it on the next reboot. The shell keeps using
; the renamed copy until it unloads it, and the installer can write to the free
; path without killing Explorer or forcing a reboot.
!macro PRIVASYNC_RELEASE_SHELL_DLL
  IfFileExists "$INSTDIR\privasync_shell.dll" 0 privasync_shell_free
    ExecWait 'regsvr32.exe /u /s "$INSTDIR\privasync_shell.dll"'
    Delete "$INSTDIR\privasync_shell.dll"
    IfFileExists "$INSTDIR\privasync_shell.dll" 0 privasync_shell_free
      ; Still there: Explorer has it mapped. Rename it out of the way.
      Delete "$INSTDIR\privasync_shell.dll.old"
      ClearErrors
      Rename "$INSTDIR\privasync_shell.dll" "$INSTDIR\privasync_shell.dll.old"
      IfErrors privasync_shell_locked
      Delete /REBOOTOK "$INSTDIR\privasync_shell.dll.old"
      Goto privasync_shell_free
    privasync_shell_locked:
      DetailPrint "privasync_shell.dll is in use and could not be moved aside."
      DetailPrint "Restart Explorer (Task Manager > Windows Explorer > Restart) and retry."
  privasync_shell_free:
!macroend

!macro NSIS_HOOK_PREINSTALL
  IfFileExists "$INSTDIR\register-context-menu.ps1" 0 privasync_preinstall_context_removed
  ; Older releases do not include the elevated helper and only accept Remove.
  IfFileExists "$INSTDIR\register-context-menu-elevated.ps1" 0 privasync_legacy_context_remove
  !insertmacro PRIVASYNC_CONTEXTMENU_ACTION PrepareInstall
  Goto privasync_preinstall_context_removed
  privasync_legacy_context_remove:
  !insertmacro PRIVASYNC_CONTEXTMENU_ACTION Remove
  privasync_preinstall_context_removed:
  !insertmacro PRIVASYNC_RELEASE_SHELL_DLL
!macroend

!macro NSIS_HOOK_POSTINSTALL
  !insertmacro PRIVASYNC_CONTEXTMENU_ACTION Install
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  !insertmacro PRIVASYNC_CONTEXTMENU_ACTION Remove
  IfFileExists "$INSTDIR\privasync_shell.dll" 0 privasync_shell_uninst_done
    Delete "$INSTDIR\privasync_shell.dll"
    IfFileExists "$INSTDIR\privasync_shell.dll" 0 privasync_shell_uninst_done
      Delete "$INSTDIR\privasync_shell.dll.old"
      ClearErrors
      Rename "$INSTDIR\privasync_shell.dll" "$INSTDIR\privasync_shell.dll.old"
      IfErrors privasync_shell_uninst_done
      Delete /REBOOTOK "$INSTDIR\privasync_shell.dll.old"
  privasync_shell_uninst_done:
!macroend
