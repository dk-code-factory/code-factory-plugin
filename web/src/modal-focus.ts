import { useLayoutEffect, useRef } from "react";

const stacks = new WeakMap<Document, HTMLElement[]>();

export function useModalFocus(close: () => void) {
  const ref = useRef<HTMLElement>(null);
  const dismiss = useRef(close);
  dismiss.current = close;
  useLayoutEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    const doc = dialog.ownerDocument;
    const opener =
      doc.activeElement instanceof HTMLElement ? doc.activeElement : undefined;
    const stack = stacks.get(doc) ?? [];
    stacks.set(doc, stack);
    stack.push(dialog);
    const isTop = () => stack.at(-1) === dialog;
    const controls = () =>
      [
        ...dialog.querySelectorAll<HTMLElement>(
          "button, a[href], input:not([type=hidden]), select, textarea, [tabindex]",
        ),
      ].filter(
        (element) =>
          element.tabIndex >= 0 &&
          !element.matches(":disabled") &&
          element.getClientRects().length > 0 &&
          getComputedStyle(element).visibility !== "hidden",
      );
    const focusFirst = () => (controls()[0] ?? dialog).focus();
    const contain = () => {
      if (!isTop()) return;
      const active = doc.activeElement;
      if (!active || !dialog.contains(active) || active.matches(":disabled"))
        focusFirst();
    };
    const keydown = (event: KeyboardEvent) => {
      if (!isTop()) return;
      if (event.key === "Escape") {
        event.preventDefault();
        event.stopPropagation();
        dismiss.current();
      } else if (event.key === "Tab") {
        const choices = controls();
        const first = choices[0] ?? dialog;
        const last = choices.at(-1) ?? dialog;
        if (
          event.shiftKey &&
          (doc.activeElement === first || !dialog.contains(doc.activeElement))
        ) {
          event.preventDefault();
          last.focus();
        } else if (
          !event.shiftKey &&
          (doc.activeElement === last || !dialog.contains(doc.activeElement))
        ) {
          event.preventDefault();
          first.focus();
        }
      }
    };
    doc.addEventListener("keydown", keydown, true);
    doc.addEventListener("focusin", contain, true);
    // Preserve a connected focused control through polling and form updates;
    // recover focus when a preparation replaces the button that had focus.
    const observer = new MutationObserver(contain);
    observer.observe(dialog, {
      childList: true,
      subtree: true,
      attributes: true,
      attributeFilter: ["disabled", "hidden"],
    });
    focusFirst();
    return () => {
      observer.disconnect();
      doc.removeEventListener("keydown", keydown, true);
      doc.removeEventListener("focusin", contain, true);
      stack.splice(stack.indexOf(dialog), 1);
      if (
        opener?.isConnected &&
        (!stack.length || stack.at(-1)!.contains(opener))
      )
        opener.focus();
    };
  }, []);
  return ref;
}
