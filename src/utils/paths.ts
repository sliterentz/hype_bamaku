import * as fs from "fs";
import * as path from "path";

export function findRepoFile(...segments: string[]): string {
  const candidates: string[] = [];

  const cwd = process.cwd();
  candidates.push(path.resolve(cwd, ...segments));
  candidates.push(path.resolve(cwd, "..", ...segments));
  candidates.push(path.resolve(cwd, "..", "..", ...segments));

  candidates.push(path.resolve(__dirname, "..", ...segments));
  candidates.push(path.resolve(__dirname, "..", "..", ...segments));
  candidates.push(path.resolve(__dirname, "..", "..", "..", ...segments));

  for (const c of candidates) {
    if (fs.existsSync(c)) return c;
  }

  throw new Error(
    `File tidak ditemukan. Coba pastikan repo lengkap dan jalankan dari root. Mencari: ${segments.join("/")}. Kandidat: ${candidates.join(", ")}`
  );
}
