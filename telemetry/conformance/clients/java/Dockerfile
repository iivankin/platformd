FROM maven:3.9.11-eclipse-temurin-21 AS build
WORKDIR /src
COPY pom.xml ./pom.xml
COPY src ./src
RUN mvn --batch-mode --quiet package
FROM eclipse-temurin:21.0.8_9-jre
COPY --from=build /src/target/conformance.jar /conformance.jar
ENTRYPOINT ["java", "-jar", "/conformance.jar"]
