import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { Grant, ManagedUser, Project } from "@/lib/api";
import { CredentialsCard, GrantEditor, USERNAME_RE, userStatus } from "./users";
import { ToastProvider } from "@/components/ui/toast";

const projects: Project[] = [
  { id: "p1", org_id: "o", name: "Alpha" },
  { id: "p2", org_id: "o", name: "Beta" },
];

function Harness({ initial = [] as Grant[], spy }: { initial?: Grant[]; spy?: (g: Grant[]) => void }) {
  const [g, setG] = useState<Grant[]>(initial);
  return (
    <ToastProvider>
      <GrantEditor
        projects={projects}
        value={g}
        onChange={(n) => {
          setG(n);
          spy?.(n);
        }}
      />
    </ToastProvider>
  );
}

describe("GrantEditor", () => {
  it("adds a project with the viewer preset and removes it again", async () => {
    const user = userEvent.setup();
    const spy = vi.fn();
    render(<Harness spy={spy} />);
    expect(screen.getByText(/will see nothing/i)).toBeInTheDocument();
    await user.selectOptions(screen.getByTestId("grant-add-select"), "p1");
    await user.click(screen.getByTestId("grant-add"));
    expect(spy).toHaveBeenLastCalledWith([{ project_id: "p1", preset: "viewer" }]);
    expect(screen.getByTestId("preset-select")).toHaveValue("viewer");
    await user.click(screen.getByRole("button", { name: /remove access to alpha/i }));
    expect(spy).toHaveBeenLastCalledWith([]);
  });

  it("custom shows the 13-area matrix prefilled from the preset and the reveal switch", async () => {
    const user = userEvent.setup();
    const spy = vi.fn();
    render(<Harness initial={[{ project_id: "p1", preset: "developer" }]} spy={spy} />);
    await user.selectOptions(screen.getByTestId("preset-select"), "custom");
    expect(screen.getAllByRole("radiogroup")).toHaveLength(13);
    const deployments = screen.getByRole("radiogroup", { name: "Deployments access" });
    expect(within(deployments).getByRole("radio", { name: "manage" })).toHaveAttribute("aria-checked", "true");
    const secrets = screen.getByRole("radiogroup", { name: "Environment & secrets access" });
    expect(within(secrets).getByRole("radio", { name: "view" })).toHaveAttribute("aria-checked", "true");
    await user.click(within(secrets).getByRole("radio", { name: "manage" }));
    await user.click(screen.getByRole("switch", { name: /reveal secret values/i }));
    const last = spy.mock.calls.at(-1)![0] as Grant[];
    expect(last[0]).toMatchObject({ project_id: "p1", preset: "custom", secrets_reveal: true });
    expect(last[0].areas?.secrets).toBe("manage");
    expect(last[0].areas?.storage).toBe("manage");
  });

  it("does not offer a project twice", async () => {
    render(<Harness initial={[{ project_id: "p1", preset: "viewer" }, { project_id: "p2", preset: "viewer" }]} />);
    expect(screen.queryByTestId("grant-add-select")).not.toBeInTheDocument();
  });
});

describe("CredentialsCard", () => {
  it("shows username and password with a one-time warning and calls done", async () => {
    const user = userEvent.setup();
    const done = vi.fn();
    render(
      <ToastProvider>
        <CredentialsCard username="rita" password="Xy7-secret-pass" onDone={done} />
      </ToastProvider>,
    );
    expect(screen.getByRole("alert")).toHaveTextContent(/will not be shown again/i);
    expect(screen.getByTestId("cred-username")).toHaveTextContent("rita");
    expect(screen.getByTestId("cred-password")).toHaveTextContent("Xy7-secret-pass");
    expect(screen.getByRole("button", { name: /copy password/i })).toBeInTheDocument();
    await user.click(screen.getByTestId("cred-done"));
    expect(done).toHaveBeenCalled();
  });
});

describe("helpers", () => {
  it("validates usernames per the contract", () => {
    for (const ok of ["abc", "a.b-c_9", "x".repeat(32)]) expect(USERNAME_RE.test(ok)).toBe(true);
    for (const bad of ["ab", "Abc", "a b", "x".repeat(33), "a@b"]) expect(USERNAME_RE.test(bad)).toBe(false);
  });
  it("derives a status lamp", () => {
    const base: ManagedUser = { id: "1", name: "n", role: "member" };
    expect(userStatus(base).label).toBe("active");
    expect(userStatus({ ...base, disabled: true }).state).toBe("idle");
    expect(userStatus({ ...base, must_change_password: true }).state).toBe("warn");
  });
});
