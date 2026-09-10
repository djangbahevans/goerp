import { describe, expect, it } from "vitest";
import { formatFileSize, matchesAccept, validateFile } from "./file-field-upload.js";

function fakeFile(name: string, type: string, sizeBytes: number): File {
  return new File([new Uint8Array(sizeBytes)], name, { type });
}

describe("matchesAccept", () => {
  it("matches by extension", () => {
    expect(matchesAccept(fakeFile("report.pdf", "application/pdf", 100), ".pdf,.docx")).toBe(true);
    expect(matchesAccept(fakeFile("report.txt", "text/plain", 100), ".pdf,.docx")).toBe(false);
  });

  it("matches by exact MIME type", () => {
    expect(matchesAccept(fakeFile("a.png", "image/png", 100), "image/png")).toBe(true);
    expect(matchesAccept(fakeFile("a.jpg", "image/jpeg", 100), "image/png")).toBe(false);
  });

  it("matches by MIME wildcard", () => {
    expect(matchesAccept(fakeFile("a.jpg", "image/jpeg", 100), "image/*")).toBe(true);
    expect(matchesAccept(fakeFile("a.csv", "text/csv", 100), "image/*")).toBe(false);
  });

  it("matches anything when accept is empty", () => {
    expect(matchesAccept(fakeFile("a.exe", "application/octet-stream", 100), "")).toBe(true);
  });
});

describe("validateFile", () => {
  it("rejects a file over the size limit", () => {
    const file = fakeFile("big.pdf", "application/pdf", 11 * 1024 * 1024);
    expect(validateFile(file, undefined, 10)).toBe("File exceeds the 10 MB limit.");
  });

  it("rejects a file that doesn't match accept", () => {
    const file = fakeFile("a.exe", "application/octet-stream", 100);
    expect(validateFile(file, "image/*", 10)).toBe("File type not accepted.");
  });

  it("accepts a valid file", () => {
    const file = fakeFile("a.png", "image/png", 100);
    expect(validateFile(file, "image/*", 10)).toBeUndefined();
  });
});

describe("formatFileSize", () => {
  it("formats bytes", () => {
    expect(formatFileSize(500)).toBe("500 B");
  });

  it("formats kilobytes", () => {
    expect(formatFileSize(2048)).toBe("2.0 KB");
  });

  it("formats megabytes", () => {
    expect(formatFileSize(5 * 1024 * 1024)).toBe("5.0 MB");
  });
});
