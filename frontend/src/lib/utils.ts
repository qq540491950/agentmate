import { createCn } from "cn/config";

// cn/config needs the project's custom text scale; otherwise text-ui is
// classified as a color and silently dropped when a text color is present.
export const cn = createCn({
  extend: {
    classGroups: {
      "font-size": [{ text: ["2xs", "3xs", "ui", "md"] }],
    },
  },
});
