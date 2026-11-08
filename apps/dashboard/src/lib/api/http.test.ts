import { describe, expect, it, vi } from "vitest";
import { ApiError, createClient, parseError, type TokenStore } from "./http";

function memoryTokens(access: string | null, refresh: string | null) {
  const s = { access, refresh };
  const store: TokenStore = {
    getAccess: () => s.access,
    getRefresh: () => s.refresh,
    set: (a, r) => {
      s.access = a;
      s.refresh = r;
    },
    clear: () => {
      s.access = null;
      s.refresh = null;
    },
  };
  return { store, s };
}

const res = (status: number, body?: unknown) => new Response(body === undefined ? null : JSON.stringify(body), { status });

describe("api client", () => {
  it("sends the bearer token and parses JSON", async () => {
    const { store } = memoryTokens("A1", "R1");
    const f = vi.fn().mockResolvedValue(res(200, { ok: true }));
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f });
    await expect(c.request("/x")).resolves.toEqual({ ok: true });
    const headers = f.mock.calls[0][1].headers as Headers;
    expect(headers.get("Authorization")).toBe("Bearer A1");
  });

  it("refreshes once for many concurrent 401s (single flight) and retries each request once", async () => {
    const { store, s } = memoryTokens("old", "R1");
    let refreshCalls = 0;
    const f = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url.endsWith("/auth/refresh")) {
        refreshCalls++;
        await new Promise((r) => setTimeout(r, 20));
        return res(200, { access_token: "new", refresh_token: "R2" });
      }
      const auth = (init?.headers as Headers).get("Authorization");
      return auth === "Bearer new" ? res(200, { url }) : res(401, { error: { code: "unauthorized", message: "expired" } });
    });
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f as typeof fetch });
    const out = await Promise.all([c.request("/a"), c.request("/b"), c.request("/c")]);
    expect(out).toHaveLength(3);
    expect(refreshCalls).toBe(1);
    expect(s.access).toBe("new");
    expect(s.refresh).toBe("R2");
    // 3 first attempts + 1 refresh + 3 retries
    expect(f).toHaveBeenCalledTimes(7);
  });

  it("does not loop: a second 401 after refresh surfaces as an ApiError", async () => {
    const { store } = memoryTokens("old", "R1");
    const f = vi.fn(async (input: RequestInfo | URL) =>
      String(input).endsWith("/auth/refresh") ? res(200, { access_token: "new", refresh_token: "R2" }) : res(401, { error: { code: "unauthorized", message: "nope" } }),
    );
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f as typeof fetch });
    await expect(c.request("/a")).rejects.toMatchObject({ status: 401, code: "unauthorized" });
    expect(f).toHaveBeenCalledTimes(3);
  });

  it("clears tokens and signals session expiry when refresh is rejected", async () => {
    const { store, s } = memoryTokens("old", "bad");
    const expired = vi.fn();
    const f = vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith("/auth/refresh") ? res(401, {}) : res(401, {})));
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f as typeof fetch, onSessionExpired: expired });
    await expect(c.request("/a")).rejects.toBeInstanceOf(ApiError);
    expect(s.access).toBeNull();
    expect(expired).toHaveBeenCalledTimes(1);
  });

  it("keeps the session when the gateway is down during refresh", async () => {
    const { store, s } = memoryTokens("old", "R1");
    const f = vi.fn(async (input: RequestInfo | URL) => (String(input).endsWith("/auth/refresh") ? res(502, {}) : res(401, {})));
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f as typeof fetch });
    await expect(c.request("/a")).rejects.toBeInstanceOf(ApiError);
    expect(s.refresh).toBe("R1");
  });

  it("turns 502 into an ApiError flagged unavailable and network failures into status 0", async () => {
    const { store } = memoryTokens("A", "R");
    const c1 = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: vi.fn().mockResolvedValue(res(502, { error: { code: "bad_gateway", message: "identity down" } })) });
    const e1 = await c1.request("/x").catch((e: ApiError) => e);
    expect(e1).toBeInstanceOf(ApiError);
    expect((e1 as ApiError).unavailable).toBe(true);
    expect((e1 as ApiError).message).toBe("identity down");
    const c2 = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: vi.fn().mockRejectedValue(new TypeError("fetch failed")) });
    const e2 = (await c2.request("/x").catch((e: ApiError) => e)) as ApiError;
    expect(e2.status).toBe(0);
    expect(e2.unavailable).toBe(true);
  });

  it("supports AbortController", async () => {
    const { store } = memoryTokens("A", "R");
    const f = vi.fn((_u: RequestInfo | URL, init?: RequestInit) => new Promise<Response>((_res, rej) => init?.signal?.addEventListener("abort", () => rej(new DOMException("x", "AbortError")))));
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f as typeof fetch });
    const ac = new AbortController();
    const p = c.request("/slow", { signal: ac.signal });
    ac.abort();
    await expect(p).rejects.toMatchObject({ aborted: true });
  });

  it("returns undefined for 204 and builds query strings without empty values", async () => {
    const { store } = memoryTokens("A", "R");
    const f = vi.fn().mockResolvedValue(res(204));
    const c = createClient({ baseUrl: "/api/v1", tokens: store, fetchImpl: f });
    await expect(c.request("/x", { query: { a: 1, b: "", c: undefined, d: "z" } })).resolves.toBeUndefined();
    expect(f.mock.calls[0][0]).toBe("/api/v1/x?a=1&d=z");
  });
});

describe("parseError", () => {
  it("understands the contract error envelope and legacy shapes", () => {
    expect(parseError(403, { error: { code: "forbidden", message: "admin only" } })).toMatchObject({ code: "forbidden", message: "admin only" });
    expect(parseError(400, { error: "plain" }).message).toBe("plain");
    expect(parseError(500, null).message).toMatch(/failed/i);
  });
});
