from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from app.api.routes.network_routes import router as network_router
from app.api.routes.user_routes import router as users_router
from app.core.config import settings

app = FastAPI(title=settings.app_name)

app.add_middleware(
    CORSMiddleware,
    allow_origins=settings.cors_origins,
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

app.include_router(users_router, prefix=settings.api_prefix)
app.include_router(network_router, prefix=settings.api_prefix)


@app.get("/")
async def read_root():
    return {"Hello": "World"}


@app.get("/health")
async def health():
    return {"status": "ok"}
