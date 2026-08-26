import { useEffect, useRef } from "react";

// Everything `aria-modal="true"` promises, kept.
//
// Declaring a dialog modal tells assistive technology that the rest of the page
// is inert. Nothing enforces that on its own: without this, focus starts on
// whatever the opener left it on, Tab walks straight out of the dialog into the
// page behind it, Escape does nothing, and closing leaves focus on the document
// body so a keyboard user has to start again from the top of the page.
//
// Four behaviours, and they are one unit because each one is useless alone:
//
//   initial focus       lands on the dialog's first focusable control
//   containment         Tab and Shift+Tab wrap inside the dialog
//   Escape              dismisses, when the caller has somewhere to dismiss to
//   restoration         focus returns to whatever opened the dialog
//
// The listener is registered on the document in the capture phase so it sees
// the key before any control inside the dialog can consume it.
const FOCUSABLE = [
  "a[href]",
  "button:not([disabled])",
  "input:not([disabled])",
  "select:not([disabled])",
  "textarea:not([disabled])",
  '[tabindex]:not([tabindex="-1"])',
].join(",");

export function useModalFocus<Element extends HTMLElement>(onDismiss?: () => void) {
  const dialog = useRef<Element>(null);
  // Held in a ref so a caller that passes a fresh closure each render does not
  // tear the listener down and rebuild it — which would move focus again.
  const dismiss = useRef(onDismiss);
  dismiss.current = onDismiss;

  useEffect(() => {
    const node = dialog.current;
    if (!node) return;
    const owner = node.ownerDocument;
    const opener = owner.activeElement as HTMLElement | null;
    // Read every time rather than once: a dialog whose fields become enabled,
    // or which grows a control, must contain the ones it has now.
    const controls = () =>
      Array.from(node.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((control) => control.tabIndex !== -1);

    (controls()[0] ?? node).focus();

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        if (!dismiss.current) return;
        event.preventDefault();
        dismiss.current();
        return;
      }
      if (event.key !== "Tab") return;
      const items = controls();
      const active = owner.activeElement;
      if (items.length === 0) {
        // A dialog with nothing to focus still may not leak focus outwards.
        event.preventDefault();
        node.focus();
        return;
      }
      const first = items[0];
      const last = items[items.length - 1];
      const outside = !node.contains(active);
      if (event.shiftKey ? active === first || outside : active === last || outside) {
        event.preventDefault();
        (event.shiftKey ? last : first).focus();
      }
    };

    owner.addEventListener("keydown", onKeyDown, true);
    return () => {
      owner.removeEventListener("keydown", onKeyDown, true);
      // Restoring beats leaving focus on the body: the opener is where the
      // operator was, and it is the control they would reach for next.
      opener?.focus?.();
    };
  }, []);

  return dialog;
}
