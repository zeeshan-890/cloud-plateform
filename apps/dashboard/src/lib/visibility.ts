/**
 * Executes a callback at a given interval, automatically pausing when the browser
 * tab is hidden to conserve CPU, memory, and network resources on low-resource hosts.
 * Automatically triggers an immediate refresh when the user focuses back on the tab.
 */
export function setVisibilityInterval(
  callback: () => void,
  intervalMs: number,
): () => void {
  if (typeof window === "undefined") {
    return () => {};
  }

  const intervalId = window.setInterval(() => {
    if (document.visibilityState === "visible") {
      callback();
    }
  }, intervalMs);

  const handleVisibilityChange = () => {
    if (document.visibilityState === "visible") {
      callback();
    }
  };

  document.addEventListener("visibilitychange", handleVisibilityChange);

  return () => {
    window.clearInterval(intervalId);
    document.removeEventListener("visibilitychange", handleVisibilityChange);
  };
}
