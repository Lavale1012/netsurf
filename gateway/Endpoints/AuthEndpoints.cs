// The NAMESPACE is what other files import — not this file's path or name.
// Convention is <RootNamespace>.<FolderPath>, so a file under Endpoints/
// gets NetMonitor.Gateway.Endpoints. Nothing enforces that; it is a
// convention that tooling and readers both rely on.
namespace NetMonitor.Gateway.Endpoints;

// A record is a concise immutable type. This is the shape the login body
// is deserialized into — Minimal APIs bind the JSON request body to it
// automatically because it is a complex type in the handler's parameters.
public record LoginRequest(string Username, string Password);

// "static class" + a first parameter marked "this" makes MapAuthEndpoints
// an extension method: callable as app.MapAuthEndpoints() even though
// WebApplication is a framework type we do not own. This is the standard
// way to keep Program.cs short while grouping routes in their own file.
public static class AuthEndpoints
{
    public static void MapAuthEndpoints(this WebApplication app)
    {
        // MapGroup is the Minimal API equivalent of [Route("...")] on a
        // controller: the prefix lives in one place, and the whole group
        // can later take .RequireAuthorization() in a single call.
        var g = app.MapGroup("/api/v1/auth");

        g.MapPost("/login", (LoginRequest req) => Results.Ok(new { data = req.Username }));
        g.MapPost("/logout", () => Results.NoContent());
        g.MapGet("/me", () => Results.Ok(new { data = "not implemented" }));
    }
}
