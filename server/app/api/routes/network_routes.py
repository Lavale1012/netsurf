from fastapi import APIRouter, HTTPException

from app.api.helpers.network.get_connections import (
    ConnectionsUnavailable,
    get_connections,
)

router = APIRouter(prefix="/network", tags=["network"])


@router.get("/connections")
async def list_connections():
    try:
        connections = get_connections()
    except ConnectionsUnavailable:
        raise HTTPException(
            status_code=503,
            detail="needs elevated privileges: run the server under sudo",
        )
    return {"data": connections}
