// Components
export { Button, type ButtonProps, buttonVariants } from './components/ui/button'
export { Input, type InputProps } from './components/ui/input'
export { Checkbox, type CheckboxProps } from './components/ui/checkbox'
export { Switch, type SwitchProps } from './components/ui/switch'
export { Slider, type SliderProps } from './components/ui/slider'
export { OtpInput, type OtpInputProps } from './components/ui/otp-input'
export { Label } from './components/ui/label'
export { Select, type SelectProps, type SelectOption } from './components/ui/select'
export { DateTimePicker, type DateTimePickerProps, type DateTimePickerLabels } from './components/ui/datetime-picker'
export { DropdownMenu, type DropdownMenuProps, type DropdownMenuItem } from './components/ui/dropdown-menu'
export {
  Toast,
  ToastClose,
  ToastDescription,
  ToastProvider,
  ToastTitle,
  ToastViewport,
  type ToastActionElement,
  type ToastProps,
} from './components/ui/toast'

export {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from './components/ui/dialog'

export { FileIcon, getFileIconKind, type FileIconKind, type FileIconProps } from './lib/fileIcon'

export { DiffView, type DiffLine, type DiffViewProps } from './components/ui/diff-view'
export {
  ConflictStrategyChooser,
  type ConflictStrategyOption,
  type ConflictStrategyChooserProps,
} from './components/ui/conflict-strategy-chooser'
export {
  ConflictVersionCompare,
  type ConflictVersionSide,
  type ConflictVersionCompareProps,
} from './components/ui/conflict-version-compare'
export { isPreviewableImage, PREVIEW_SIZE_LIMIT } from './lib/conflictCompare'

// Utils
export { cn } from './lib/utils'

// Theme
export { setThemePreference, getThemePreference, initTheme, type ThemePreference } from './lib/theme'
