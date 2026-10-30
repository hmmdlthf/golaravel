<?php

ignore_user_abort(true);

$handler = function () {
    $uri = parse_url($_SERVER['REQUEST_URI'] ?? '/', PHP_URL_PATH);
    header('Content-Type: application/json; charset=utf-8');

    if ($uri === '/api/user') {
        echo json_encode([
            'id' => 101,
            'name' => 'Taylor Otwell',
            'email' => 'taylor@laravel.com',
            'role' => 'Administrator',
            'is_active' => true,
        ]);
        return;
    }

    if (preg_match('#^/api/users/([^/]+)/posts/([^/]+)$#', $uri, $matches)) {
        echo json_encode([
            'user_id' => $matches[1],
            'post_id' => $matches[2],
            'status' => 'published',
        ]);
        return;
    }

    http_response_code(404);
    echo json_encode(['error' => 'not found']);
};

for ($nbRequests = 0; $nbRequests < 10000; ++$nbRequests) {
    $keepRunning = \frankenphp_handle_request($handler);
    gc_collect_cycles();
    if (!$keepRunning) {
        break;
    }
}
