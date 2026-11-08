import type { SVGProps } from "react";

/** Hand-drawn 24px stroke icon set. Decorative by default (aria-hidden). */
const PATHS = {
  home: "M3 11.5 12 4l9 7.5M5.5 10v9.5h5v-5h3v5h5V10",
  folder: "M3 7a2 2 0 0 1 2-2h4l2 2.5h8a2 2 0 0 1 2 2V18a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
  rocket: "M12 3c3.5 1.5 5.5 5 5.5 9l-2 2.5h-7L6.5 12c0-4 2-7.5 5.5-9zM9 17.5 7.5 21 12 19l4.5 2L15 17.5M12 9.5a1.5 1.5 0 1 0 0 .01",
  hammer: "m14 6 4 4-9.5 9.5a2 2 0 0 1-2.8 0l-1.2-1.2a2 2 0 0 1 0-2.8zM12 4l3-1 6 6-1 3-3-3-1 1-4-4 1-1z",
  server: "M4 4h16v6H4zM4 14h16v6H4zM7.5 7h.01M7.5 17h.01M12 7h5M12 17h5",
  globe: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM3 12h18M12 3c2.7 2.4 4 5.4 4 9s-1.3 6.6-4 9c-2.7-2.4-4-5.4-4-9s1.3-6.6 4-9z",
  key: "M14.5 9.5a4.5 4.5 0 1 1-2.2 3.9L4 21v-3l2-.5.5-2 2.3-.4 3.6-3.6A4.5 4.5 0 0 1 14.5 9.5zM16.5 8h.01",
  puzzle: "M10 4h4v3a2 2 0 1 0 0 4h3v3a2 2 0 1 1 0 4h-3v-0H10v-3a2 2 0 1 0-4 0v3H4v-6h3a2 2 0 1 0 0-4H4V4z",
  database: "M4 6c0-1.7 3.6-3 8-3s8 1.3 8 3-3.6 3-8 3-8-1.3-8-3zM4 6v6c0 1.7 3.6 3 8 3s8-1.3 8-3V6M4 12v6c0 1.7 3.6 3 8 3s8-1.3 8-3v-6",
  bucket: "M5 8h14l-1.5 11a2 2 0 0 1-2 1.7h-7a2 2 0 0 1-2-1.7zM5 8c0-1.7 3.1-3 7-3s7 1.3 7 3-3.1 3-7 3-7-1.3-7-3z",
  chart: "M4 20V5M4 20h16M8 16l3.5-5 3 3L20 7",
  logs: "M5 4h14v16H5zM8.5 8.5h7M8.5 12h7M8.5 15.5h4",
  chip: "M7 7h10v10H7zM10 3v4M14 3v4M10 17v4M14 17v4M3 10h4M3 14h4M17 10h4M17 14h4M10 10h4v4h-4z",
  card: "M3 6h18v12H3zM3 10h18M6.5 15h3",
  branch: "M7 4v10M7 18a2 2 0 1 0 0 .01M17 6a2 2 0 1 0 0 .01M7 14c0-3 2-4 6-4h2.5a1.5 1.5 0 0 0 1.5-1.5V8",
  shield: "M12 3 4.5 6v5.5c0 4.4 3 8 7.5 9.5 4.5-1.5 7.5-5.1 7.5-9.5V6zM9 12l2.2 2.2L15.5 10",
  users: "M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2.5 20c.5-3.5 3-5.5 6.5-5.5s6 2 6.5 5.5M16 4.5a3.2 3.2 0 0 1 0 6.2M18 14.8c1.9.7 3.2 2.4 3.5 5.2",
  gear: "M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM19.4 13.5l1.6 1.2-1.8 3.1-1.9-.6a7 7 0 0 1-1.7 1l-.3 2h-3.6l-.3-2a7 7 0 0 1-1.7-1l-1.9.6L5 14.7l1.6-1.2a7 7 0 0 1 0-3L5 9.3l1.8-3.1 1.9.6a7 7 0 0 1 1.7-1l.3-2h3.6l.3 2c.6.2 1.2.6 1.7 1l1.9-.6L21 9.3l-1.6 1.2a7 7 0 0 1 0 3z",
  lock: "M6 11h12v9H6zM8.5 11V8a3.5 3.5 0 0 1 7 0v3M12 15v2",
  user: "M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4.5 20.5c.6-4 3.5-6 7.5-6s6.9 2 7.5 6",
  logout: "M10 4H5v16h5M14 8l4 4-4 4M18 12H9",
  search: "M10.5 17a6.5 6.5 0 1 0 0-13 6.5 6.5 0 0 0 0 13zM15.5 15.5 20 20",
  sun: "M12 16.5a4.5 4.5 0 1 0 0-9 4.5 4.5 0 0 0 0 9zM12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.6 1.6M17.1 17.1l1.6 1.6M18.7 5.3l-1.6 1.6M6.9 17.1l-1.6 1.6",
  moon: "M20 14.5A8 8 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z",
  copy: "M8 8h11v12H8zM5 16V4h11",
  check: "m5 12.5 4.5 4.5L19 7.5",
  x: "M6 6l12 12M18 6 6 18",
  plus: "M12 5v14M5 12h14",
  trash: "M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 11v6M14 11v6",
  refresh: "M20 11a8 8 0 0 0-14.5-3.5L4 9M4 4v5h5M4 13a8 8 0 0 0 14.5 3.5L20 15M20 20v-5h-5",
  play: "M8 5v14l11-7z",
  stop: "M6 6h12v12H6z",
  restart: "M5 12a7 7 0 1 1 2 5M5 18v-5h5",
  eye: "M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12zM12 14.5a2.5 2.5 0 1 0 0-5 2.5 2.5 0 0 0 0 5z",
  eyeOff: "M3 3l18 18M10 6a9 9 0 0 1 2-.5c6 0 9.5 6.5 9.5 6.5a16 16 0 0 1-3 3.8M6.5 7.5C4 9.3 2.5 12 2.5 12S6 18.5 12 18.5c1.4 0 2.7-.4 3.8-.9M9.9 9.9a2.5 2.5 0 0 0 3.2 3.2",
  download: "M12 4v11M7.5 11 12 15.5 16.5 11M5 20h14",
  upload: "M12 16V5M7.5 9 12 4.5 16.5 9M5 20h14",
  alert: "M12 4 2.5 20h19zM12 10v4.5M12 17.2h.01",
  info: "M12 21a9 9 0 1 0 0-18 9 9 0 0 0 0 18zM12 11v6M12 7.8h.01",
  chevron: "m9 6 6 6-6 6",
  chevronDown: "m6 9 6 6 6-6",
  menu: "M4 7h16M4 12h16M4 17h16",
  more: "M5 12h.01M12 12h.01M19 12h.01",
  external: "M14 4h6v6M20 4 10.5 13.5M18 14v5H5V6h5",
  terminal: "M4 5h16v14H4zM7.5 9.5 10 12l-2.5 2.5M12 15h4.5",
  command: "M9 9V6.5A2.5 2.5 0 1 0 6.5 9H9zm0 0h6m-6 0v6m6-6V6.5A2.5 2.5 0 1 1 17.5 9H15zm0 0v6m0 0h2.5a2.5 2.5 0 1 1-2.5 2.5V15zm0 0H9m0 0H6.5A2.5 2.5 0 1 0 9 17.5V15z",
  pen: "M4 20l1-4L16.5 4.5a2 2 0 0 1 2.8 0l.2.2a2 2 0 0 1 0 2.8L8 19zM14 7l3 3",
  link: "M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1",
  box: "M12 3 4 7v10l8 4 8-4V7zM4 7l8 4 8-4M12 11v10",
  bolt: "M13 3 5 13.5h6L10 21l8-10.5h-6z",
} as const;

export type IconName = keyof typeof PATHS;

/** Stroke weight, caps, fill and the "chip" behind nav icons are set per visual style through the `.jp-icon` class. */
export function Icon({ name, size = 20, className, ...rest }: { name: IconName; size?: number } & Omit<SVGProps<SVGSVGElement>, "name">) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      className={className ? `jp-icon ${className}` : "jp-icon"}
      {...rest}
    >
      <path d={PATHS[name]} />
    </svg>
  );
}
