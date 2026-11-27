import { useEffect } from "react";
import { createPortal } from "react-dom";

interface ModalOverlayProps {
  /** Called when the user clicks the dim background. */
  onDismiss?: () => void;
  children:    React.ReactNode;
}

/**
 * Renders `children` centered over a dim full-window backdrop.
 *
 * Portals to `document.body` — without that, a `fixed inset-0` overlay
 * inside Tauri's webview layout can fail to cover the whole window
 * (subtle stacking-context quirks from the tab/main flex chain).
 */
export default function ModalOverlay({ onDismiss, children }: ModalOverlayProps) {
  // Lock background scroll while the modal is open.
  useEffect(() => {
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => { document.body.style.overflow = prev; };
  }, []);

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      onClick={(e) => {
        if (onDismiss && e.target === e.currentTarget) onDismiss();
      }}
    >
      {children}
    </div>,
    document.body,
  );
}
