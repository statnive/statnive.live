import { useEffect, useRef, useState } from 'preact/hooks';

interface CopyButtonProps {
  text: string;
  label?: string;
  copiedLabel?: string;
}

export default function CopyButton({
  text,
  label = 'Copy',
  copiedLabel = 'Copied!',
}: CopyButtonProps) {
  const [copied, setCopied] = useState(false);
  const timerRef = useRef<number | null>(null);

  useEffect(
    () => () => {
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
    },
    [],
  );

  async function onCopy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      if (timerRef.current !== null) window.clearTimeout(timerRef.current);
      timerRef.current = window.setTimeout(() => {
        setCopied(false);
        timerRef.current = null;
      }, 2000);
    } catch {
      // clipboard unavailable; the original text is still visible in the DOM.
    }
  }

  return (
    <button type="button" class="statnive-chip" onClick={() => void onCopy()}>
      {copied ? copiedLabel : label}
    </button>
  );
}
