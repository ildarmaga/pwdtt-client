import { ClipboardGetText } from '../../../wailsjs/runtime/runtime';

function isTextField(el: EventTarget | null): el is HTMLInputElement | HTMLTextAreaElement {
  if (!el || !(el instanceof HTMLElement)) return false;
  if (el instanceof HTMLTextAreaElement) return true;
  if (el instanceof HTMLInputElement) {
    const t = el.type;
    return t === 'text' || t === 'password' || t === 'url' || t === 'search' || t === 'tel' || t === '';
  }
  return el.isContentEditable;
}

export function isPasteTargetEditable(target: EventTarget | null): boolean {
  return isTextField(target);
}

export function mergePasteValue(
  start: number,
  end: number,
  current: string,
  text: string,
): string {
  return current.slice(0, start) + text + current.slice(end);
}

export async function handleControlledPaste(
  e: React.ClipboardEvent<HTMLInputElement | HTMLTextAreaElement>,
  current: string,
  apply: (next: string) => void,
) {
  const el = e.currentTarget;
  const start = el.selectionStart ?? current.length;
  const end = el.selectionEnd ?? current.length;

  let text = e.clipboardData?.getData('text/plain') ?? '';
  if (!text) {
    try {
      text = await navigator.clipboard.readText();
    } catch {
      try {
        text = await ClipboardGetText();
      } catch {
        text = '';
      }
    }
  }
  if (!text) return;
  e.preventDefault();
  apply(mergePasteValue(start, end, current, text));
}
