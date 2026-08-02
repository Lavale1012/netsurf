from fastapi import APIRouter

router = APIRouter(prefix="/users", tags=["users"])


@router.get("/")
async def get_users():
    return [{"username": "Alice"}, {"username": "Bob"}]


@router.get("/{user_id}")
async def get_user(user_id: int):
    return {"user_id": user_id}
