from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    app_name: str = "net-monitor"
    api_prefix: str = "/api/v1"
    cors_origins: list[str] = ["http://localhost:5173"]


settings = Settings()
