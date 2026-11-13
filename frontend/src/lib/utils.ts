import { createCn } from "cn/config";

// cn/config 需要项目自定义的文本尺度；否则 text-ui
// 会被归类为颜色，并在存在文本颜色时被静默丢弃。
export const cn = createCn({
  extend: {
    classGroups: {
      "font-size": [{ text: ["2xs", "3xs", "ui", "md"] }],
    },
  },
});
