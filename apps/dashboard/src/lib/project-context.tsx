"use client";

import React, { createContext, useContext, useEffect, useState } from "react";
import type { Project } from "@/lib/types";

interface ProjectContextValue {
  activeProject: Project | null;
  setActiveProject: (p: Project | null) => void;
  rootDomain: string;
  setRootDomain: (d: string) => void;
  panelDomain: string;
  getAppPublicUrl: (projectName: string) => string;
}

const ProjectContext = createContext<ProjectContextValue>({
  activeProject: null,
  setActiveProject: () => {},
  rootDomain: "jp.localhost",
  setRootDomain: () => {},
  panelDomain: "panel.jp.localhost",
  getAppPublicUrl: () => "",
});

export function ProjectProvider({ children }: { children: React.ReactNode }) {
  const [activeProject, setActiveProject] = useState<Project | null>(null);
  const [rootDomain, setRootDomainState] = useState<string>("jp.localhost");

  useEffect(() => {
    if (typeof window !== "undefined") {
      const savedDomain = localStorage.getItem("jp_platform_domain");
      if (savedDomain) {
        setRootDomainState(savedDomain);
      } else {
        const host = window.location.hostname || "localhost";
        // If accessed via panel.domain.com, the base domain for customer apps is domain.com
        const detectedBase = host.startsWith("panel.") ? host.replace(/^panel\./, "") : host;
        setRootDomainState(
          detectedBase === "localhost" || /^[0-9.]+$/.test(detectedBase)
            ? "jp.localhost"
            : detectedBase
        );
      }
    }
  }, []);

  const setRootDomain = (domain: string) => {
    let clean = domain.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
    if (clean.startsWith("panel.")) {
      clean = clean.replace(/^panel\./, "");
    }
    setRootDomainState(clean);
    if (typeof window !== "undefined") {
      localStorage.setItem("jp_platform_domain", clean);
    }
  };

  const cleanBase = rootDomain.startsWith("panel.") ? rootDomain.replace(/^panel\./, "") : rootDomain;
  const panelDomain =
    cleanBase === "jp.localhost" || cleanBase === "localhost"
      ? "panel.jp.localhost"
      : `panel.${cleanBase}`;

  const getAppPublicUrl = (projectName: string) => {
    const slug = projectName.toLowerCase().replace(/[^a-z0-9-]/g, "-").replace(/-+/g, "-");
    return `http://${slug}.${cleanBase}`;
  };

  return (
    <ProjectContext.Provider
      value={{
        activeProject,
        setActiveProject,
        rootDomain,
        setRootDomain,
        panelDomain,
        getAppPublicUrl,
      }}
    >
      {children}
    </ProjectContext.Provider>
  );
}

export function useProject() {
  return useContext(ProjectContext);
}
