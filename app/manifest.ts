import type { MetadataRoute } from "next";

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: "Snowball-Voice-Gate",
    short_name: "Snowball-Voice-Gate",
    description: "Private ChatGPT Voice gateway and recovery console.",
    start_url: "/",
    display: "standalone",
    background_color: "#080a0c",
    theme_color: "#080a0c",
    orientation: "portrait",
    icons: [{ src: "/branding/icon.png", sizes: "1254x1254", type: "image/png", purpose: "any" }],
  };
}
