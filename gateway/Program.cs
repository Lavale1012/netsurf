// using directives must come before any executable code. Most common
// namespaces are already imported project-wide by <ImplicitUsings>enable</>
// in the .csproj, which is why there is no using for WebApplication itself.
using NetMonitor.Gateway.Endpoints;

var builder = WebApplication.CreateBuilder(args);

// YARP forwards the live routes to the Go service. It handles WebSocket
// upgrades itself via IHttpUpgradeFeature, so app.UseWebSockets() is not
// needed here — that middleware is for terminating sockets in this app.
builder.Services.AddReverseProxy()
    .LoadFromConfig(builder.Configuration.GetSection("ReverseProxy"));
// Only needed when Vite is run without its dev proxy. With the proxy (the
// normal path) every request reaches the gateway server-side, where CORS
// does not apply. AllowCredentials forbids a wildcard origin, so the list
// is explicit.
if (builder.Environment.IsDevelopment())
{
    builder.Services.AddCors(options => options.AddDefaultPolicy(policy => policy
        .WithOrigins("http://localhost:5173", "http://127.0.0.1:5173")
        .AllowCredentials()
        .AllowAnyHeader()
        .AllowAnyMethod()));
}

var app = builder.Build();
// One line per endpoint group, each defined in its own file under Endpoints/.
app.MapAuthEndpoints();

if (app.Environment.IsDevelopment())
{
    app.UseCors();
}

// Gateway liveness. Distinct from the Go service's /health, which reports
// hub state and is reachable at /api/v1/stream/health.
app.MapGet("/health", () => Results.Ok(new { status = "ok", service = "gateway" }));

app.MapReverseProxy();

app.Run();
