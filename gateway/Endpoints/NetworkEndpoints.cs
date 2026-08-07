namespace NetMonitor.Gateway.Endpoints;

public static class NetworkEndpoints
{
    public static void MapNetworkEndpoints(this WebApplication app)
    {
        // Implementation for mapping network endpoints
        var g = app.MapGroup("/api/v1/network");

        g.MapGet("/status", () => "Network is healthy!");
        g.MapGet("/find-device", () => "Device details go here!");
    }
}