<?php

require __DIR__ . '/vendor/autoload.php';

$case = getenv('CONFORMANCE_CASE') ?: throw new RuntimeException('CONFORMANCE_CASE is required');
$dsn = getenv('SENTRY_DSN') ?: throw new RuntimeException('SENTRY_DSN is required');
Sentry\init(['dsn' => $dsn]);
Sentry\configureScope(static function (Sentry\State\Scope $scope) use ($case): void {
    $scope->setTag('conformance_case', $case);
});
Sentry\captureException(new RuntimeException('php conformance'));
Sentry\flush(10);
