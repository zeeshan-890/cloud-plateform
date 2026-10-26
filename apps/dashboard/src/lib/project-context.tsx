"use client";

import React, { createContext, useContext, useEffect, useState } from "react";
import type { Project } from "@/lib/types";

interface ProjectContextValue {
  activeProject: Project | null;
  setActiveProject: (p: Project | null) => void;
  rootDomain: string;
  setRootDomain: (d: string) => void;
  getAppPublicUrl: (projectName: string) => string;
}

const ProjectContext = createContext<ProjectContextValue>({
  activeProject: null,
  setActiveProject: () => {},
  rootDomain: "jp.localhost",
  setRootDomain: () => {},
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
        setRootDomainState(host === "localhost" ? "jp.localhost" : host);
      }
    }
  }, []);

  const setRootDomain = (domain: string) => {
    const clean = domain.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
    setRootDomainState(clean);
    if (typeof window !== "undefined") {
      localStorage.setItem("jp_platform_domain", clean);
    }
  };

  const getAppPublicUrl = (projectName: string) => {
    const slug = projectName.toLowerCase().replace(/[^a-z0-9-]/g, "-").replace(/-+/g, "-");
    return `http://${slug}.${rootDomain}`;
  };

  return (
    <ProjectContext.Provider
      value={{
        activeProject,
        setActiveProject,
        rootDomain,
        setRootDomain,
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
