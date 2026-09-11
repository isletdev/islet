import os
import time

from fastapi import FastAPI

app = FastAPI(title="Islet sample")
started = time.time()


@app.get("/health")
def health():
    return "ok"


@app.get("/")
def index():
    return {"greeting": os.environ.get("GREETING", "hello"), "uptimeSeconds": int(time.time() - started)}
