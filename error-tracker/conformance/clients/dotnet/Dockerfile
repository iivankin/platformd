FROM mcr.microsoft.com/dotnet/sdk:9.0.305 AS build
WORKDIR /src
COPY Conformance.csproj Program.cs ./
RUN dotnet publish --configuration Release --output /out
FROM mcr.microsoft.com/dotnet/runtime:9.0.9
COPY --from=build /out /app
ENTRYPOINT ["dotnet", "/app/Conformance.dll"]
