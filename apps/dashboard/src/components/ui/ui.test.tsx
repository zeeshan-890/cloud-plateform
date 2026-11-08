import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ConfirmDialog } from "./modal";
import { Switch } from "./switch";
import { DataTable } from "./table";
import { ToastProvider, useToast } from "./toast";
import { Tabs } from "./tabs";
import { LcdCounter } from "./lcd";

function SwitchHarness() {
  const [on, setOn] = useState(false);
  return <Switch checked={on} onChange={setOn} label="Auto deploy" />;
}

describe("Switch", () => {
  it("toggles with click and keyboard and exposes aria-checked", async () => {
    const user = userEvent.setup();
    render(<SwitchHarness />);
    const sw = screen.getByRole("switch", { name: /auto deploy/i });
    expect(sw).toHaveAttribute("aria-checked", "false");
    await user.click(sw);
    expect(sw).toHaveAttribute("aria-checked", "true");
    sw.focus();
    await user.keyboard(" ");
    expect(sw).toHaveAttribute("aria-checked", "false");
  });

  it("does not toggle when disabled", async () => {
    const onChange = vi.fn();
    render(<Switch checked={false} onChange={onChange} label="Locked" disabled />);
    await userEvent.setup().click(screen.getByRole("switch"));
    expect(onChange).not.toHaveBeenCalled();
  });
});

describe("ConfirmDialog type-to-confirm", () => {
  it("keeps the confirm button disabled until the exact text is typed, then runs the action", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn().mockResolvedValue(undefined);
    const onClose = vi.fn();
    render(<ConfirmDialog open title="Delete project" body="Gone forever" confirmLabel="Delete it" requireText="my-project" onConfirm={onConfirm} onClose={onClose} />);
    const btn = screen.getByRole("button", { name: "Delete it" });
    expect(btn).toBeDisabled();
    const input = screen.getByRole("textbox");
    await user.type(input, "my-proj");
    expect(btn).toBeDisabled();
    await user.type(input, "ect");
    expect(btn).toBeEnabled();
    await user.click(btn);
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it("shows the error and stays open when the action fails", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<ConfirmDialog open title="Delete" body="x" confirmLabel="Go" onConfirm={() => Promise.reject(new Error("backend said no"))} onClose={onClose} />);
    await user.click(screen.getByRole("button", { name: "Go" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("backend said no");
    expect(onClose).not.toHaveBeenCalled();
  });

  it("traps focus inside the dialog and closes on Escape", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(
      <>
        <button>outside</button>
        <ConfirmDialog open title="T" body="b" confirmLabel="OK" onConfirm={() => undefined} onClose={onClose} />
      </>,
    );
    const dialog = screen.getByRole("dialog");
    for (let i = 0; i < 6; i++) {
      await user.tab();
      expect(dialog.contains(document.activeElement)).toBe(true);
    }
    await user.keyboard("{Escape}");
    expect(onClose).toHaveBeenCalled();
  });
});

function ToastHarness() {
  const toast = useToast();
  return (
    <>
      <button onClick={() => toast.success("Saved", "All good")}>ok</button>
      <button onClick={() => toast.error("Failed", new Error("boom"))}>bad</button>
    </>
  );
}

describe("Toast", () => {
  it("announces success politely and errors as alerts, and can be dismissed", async () => {
    const user = userEvent.setup();
    render(
      <ToastProvider>
        <ToastHarness />
      </ToastProvider>,
    );
    await user.click(screen.getByText("ok"));
    expect(screen.getByRole("status")).toHaveTextContent("Saved");
    await user.click(screen.getByText("bad"));
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
    await user.click(screen.getAllByRole("button", { name: /dismiss/i })[0]);
    await act(async () => {
      await new Promise((r) => setTimeout(r, 300));
    });
    expect(screen.queryByText("Saved")).not.toBeInTheDocument();
  });
});

describe("DataTable", () => {
  const cols = [
    { key: "n", header: "Name", cell: (r: { n: string; v: number }) => r.n },
    { key: "v", header: "Value", cell: (r: { n: string; v: number }) => r.v },
  ];
  it("renders captioned rows", () => {
    render(<DataTable caption="Things" columns={cols} rows={[{ n: "a", v: 1 }, { n: "b", v: 2 }]} rowKey={(r) => r.n} />);
    expect(screen.getByRole("table", { name: "Things" })).toBeInTheDocument();
    expect(screen.getAllByRole("row")).toHaveLength(3);
    expect(screen.getByRole("columnheader", { name: "Value" })).toBeInTheDocument();
  });
  it("shows the empty state instead of an empty table", () => {
    render(<DataTable caption="Things" columns={cols} rows={[]} rowKey={(r) => r.n} empty={<p>Nothing here</p>} />);
    expect(screen.getByText("Nothing here")).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("Tabs", () => {
  it("moves selection with arrow keys", async () => {
    const user = userEvent.setup();
    function H() {
      const [v, setV] = useState("a");
      return <Tabs label="T" value={v} onChange={setV} items={[{ id: "a", label: "A" }, { id: "b", label: "B" }]} />;
    }
    render(<H />);
    screen.getByRole("tab", { name: "A" }).focus();
    await user.keyboard("{ArrowRight}");
    expect(screen.getByRole("tab", { name: "B" })).toHaveAttribute("aria-selected", "true");
    fireEvent.keyDown(screen.getByRole("tab", { name: "B" }), { key: "Home" });
    expect(screen.getByRole("tab", { name: "A" })).toHaveAttribute("aria-selected", "true");
  });
});

describe("LcdCounter", () => {
  it("exposes the value to assistive tech", () => {
    render(<LcdCounter label="Projects" value={12} unit="pcs" />);
    expect(screen.getByRole("group", { name: "Projects: 12 pcs" })).toBeInTheDocument();
  });
  it("falls back to text for non-numeric values", () => {
    render(<LcdCounter label="Load" value="n/a" />);
    expect(screen.getByText("n/a")).toBeInTheDocument();
  });
});
