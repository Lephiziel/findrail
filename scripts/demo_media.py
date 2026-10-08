#!/usr/bin/env python3
"""Render a short walkthrough from real alpha CLI/API results on synthetic files.

Developer-only dependencies: Pillow, DejaVu fonts and ffmpeg. This is a rendered
walkthrough, not a screen recording or a performance benchmark.
"""
import argparse
import json
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import textwrap
import time
import urllib.error
import urllib.parse
import urllib.request

from PIL import Image, ImageDraw, ImageFont


def capture(binary, repo):
    with tempfile.TemporaryDirectory(prefix="findrail-media-") as temporary:
        root = Path(temporary)
        shutil.copytree(repo / "examples/demo", root / "demo")

        def run(command, *args):
            result = subprocess.run(
                [str(binary), command, "--data-dir", str(root / "index"), *args],
                check=True, capture_output=True, text=True, encoding="utf-8", timeout=30)
            return json.loads(result.stdout)

        indexed = run("index", "--json", str(root / "demo"))
        found = run("search", "--json", "idempotency")
        assert indexed["seen"] == found["total"] == 3
        pdf = next(item for item in found["results"] if item["path"].endswith(".pdf"))
        assert pdf["page"] == 2 and pdf["uri"].endswith("#page=2")
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        process = subprocess.Popen(
            [str(binary), "serve", "--data-dir", str(root / "index"),
             "--addr", f"127.0.0.1:{port}"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

        def get(path):
            with urllib.request.urlopen(f"http://127.0.0.1:{port}" + path, timeout=3) as response:
                return json.load(response)

        def wait_until(fn):
            deadline = time.monotonic() + 15
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError("Demo server exited")
                try:
                    result = fn()
                    if result:
                        return result
                except (urllib.error.URLError, TimeoutError):
                    pass
                time.sleep(0.1)
            raise RuntimeError("Demo did not reach the expected state")

        try:
            wait_until(lambda: get("/healthz")["status"] == "ok")
            preview = get("/api/v1/documents/" + pdf["id"] + "?page=2")
            assert preview["page"] == 2 and "Idempotency" in preview["text"]
            empty = get("/api/v1/search?q=cobalt")
            assert empty["total"] == 0
            note = root / "demo/retry-notes.md"
            note.write_text(note.read_text(encoding="utf-8").replace(
                "Demo marker: amber", "Demo marker: cobalt"), encoding="utf-8")
            fresh = wait_until(lambda: (result if (result := get(
                "/api/v1/search?q=cobalt"))["total"] == 1 else None))
            assert fresh["results"][0]["path"] == "retry-notes.md"
            return found, preview, fresh
        finally:
            process.terminate()
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait()


def render(found, preview, fresh, output, fonts):
    regular = str(fonts / "DejaVuSans.ttf")
    bold = str(fonts / "DejaVuSans-Bold.ttf")
    mono = str(fonts / "DejaVuSansMono.ttf")
    font = lambda size, face=regular: ImageFont.truetype(face, size)
    ink, muted, green = "#f4f5ee", "#a6b5b0", "#a2e7c7"
    frames = []

    def base(title, step):
        image = Image.new("RGB", (1280, 720), "#102824")
        draw = ImageDraw.Draw(image)
        draw.text((56, 38), "findrail", font=font(34, bold), fill=green)
        draw.text((1084, 46), "LOCAL ALPHA", font=font(15, bold), fill=muted)
        draw.text((56, 111), title, font=font(43, bold), fill=ink)
        draw.text((56, 170), "Notes + code + text PDFs. No document uploads.", font=font(22), fill=muted)
        draw.rounded_rectangle((56, 226, 1224, 626), radius=20, fill="#193c34", outline="#3b6456", width=2)
        draw.text((56, 662), "github.com/Lephiziel/findrail", font=font(20, mono), fill=green)
        draw.text((827, 665), "Real output · rendered walkthrough", font=font(16), fill=muted)
        for number in range(4):
            draw.rounded_rectangle((1120 + number * 24, 674, 1134 + number * 24, 680), radius=3,
                                   fill=green if number == step else "#3b6456")
        return image, draw

    image, draw = base("Remember a word. Find its source.", 0)
    draw.text((88, 260), '$ findrail search "idempotency"', font=font(27, mono), fill=green)
    draw.text((88, 315), f'{found["total"]} matching documents', font=font(24, bold), fill=ink)
    for number, item in enumerate(found["results"]):
        y = 371 + number * 73
        label = item["path"] + (f'  ·  PDF page {item["page"]}' if item.get("page") else "")
        draw.text((88, y), label, font=font(25, mono), fill=ink)
        snippet = " ".join(item["snippet"].split())
        draw.text((88, y + 33), textwrap.shorten(snippet, width=85, placeholder="…"), font=font(19), fill=muted)
    frames.append(image)
    image, draw = base("Check the match on the original page.", 1)
    draw.text((88, 260), "webhook-runbook.pdf", font=font(29, mono), fill=green)
    draw.text((88, 318), f'Indexed text preview · page {preview["page"]} of {preview["page_count"]}', font=font(23), fill=muted)
    for number, line in enumerate(textwrap.wrap(preview["text"], width=67)):
        draw.text((88, 380 + number * 37), line, font=font(26), fill=ink)
    draw.text((88, 550), "Original location ends in webhook-runbook.pdf#page=2", font=font(23, mono), fill=green)
    frames.append(image)
    image, draw = base("Edit a note. The index follows.", 2)
    draw.text((88, 260), 'Search "cobalt": 0 matches before the edit', font=font(26, mono), fill=muted)
    draw.text((88, 332), "retry-notes.md", font=font(28, mono), fill=green)
    draw.text((88, 389), "- Demo marker: amber", font=font(31, mono), fill="#f0b8a5")
    draw.text((88, 446), "+ Demo marker: cobalt", font=font(31, mono), fill=green)
    draw.text((88, 550), "The server stays running while the file changes.", font=font(23), fill=ink)
    frames.append(image)
    image, draw = base("New text. Ready to search.", 3)
    draw.text((88, 260), '$ findrail search "cobalt"', font=font(27, mono), fill=green)
    draw.text((88, 328), f'{fresh["total"]} matching document', font=font(29, bold), fill=ink)
    draw.text((88, 397), fresh["results"][0]["path"], font=font(28, mono), fill=green)
    draw.text((88, 448), "Demo marker: [cobalt]", font=font(26, mono), fill=ink)
    draw.text((88, 550), "Linux / macOS / Windows · open source · built in Go", font=font(24), fill=muted)
    frames.append(image)
    output.mkdir(parents=True, exist_ok=True)
    frames[0].save(output / "demo.png", optimize=True)
    frames[0].save(output / "demo.gif", save_all=True, append_images=frames[1:],
                   duration=[5000, 5000, 4000, 5000], loop=0, optimize=False)
    with tempfile.TemporaryDirectory(prefix="findrail-frames-") as temporary:
        root = Path(temporary)
        concat = []
        for number, (frame, duration) in enumerate(zip(frames, [5, 5, 4, 5])):
            frame.save(root / f"frame-{number}.png")
            concat.extend([f"file 'frame-{number}.png'", f"duration {duration}"])
        concat.append("file 'frame-3.png'")
        (root / "frames.txt").write_text("\n".join(concat))
        subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y",
                        "-f", "concat", "-safe", "0", "-i", str(root / "frames.txt"),
                        "-vf", "fps=24", "-c:v", "libx264", "-pix_fmt", "yuv420p",
                        "-movflags", "+faststart", str((output / "demo.mp4").resolve())], check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("--output", type=Path, default=Path("dist/rendered-demo"))
    parser.add_argument("--fonts", type=Path, default=Path("/usr/share/fonts/truetype/dejavu"))
    args = parser.parse_args()
    repo = Path(__file__).resolve().parents[1]
    found, preview, fresh = capture(args.binary.resolve(strict=True), repo)
    render(found, preview, fresh, args.output, args.fonts)
    print("Validated real search, PDF page 2 and live refresh. Created demo.png, demo.gif and demo.mp4.")


if __name__ == "__main__":
    main()
