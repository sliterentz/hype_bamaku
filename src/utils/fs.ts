import * as fs from "fs";

export function readFileUtf8(path: string): string {
  return fs.readFileSync(path, { encoding: "utf8" });
}
