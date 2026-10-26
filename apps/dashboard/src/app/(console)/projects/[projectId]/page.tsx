import ProjectDetailClient from "./client";

export async function generateStaticParams() {
  return [{ projectId: "default" }];
}

export default function Page() {
  return <ProjectDetailClient />;
}
