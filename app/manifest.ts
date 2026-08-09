import type { MetadataRoute } from "next";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Snowball",
    short_name: "Snowball",
    description: "Private ChatGPT Voice gateway and recovery console.",
    start_url: "/",
    display: "standalone",
    background_color: "#080a0c",
    theme_color: "#080a0c",
    orientation: "portrait",
  };
}
