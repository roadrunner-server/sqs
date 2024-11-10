<?php

ini_set('display_errors', 'stderr');
require dirname(__DIR__) . "/vendor/autoload.php";

$consumer = new Spiral\RoadRunner\Jobs\Consumer();

while ($task = $consumer->waitTask()) {
    $headers = $task->getHeaders();
    $total_attempts = (int)$task->getHeaderLine("attempts") + 1;

    if ($total_attempts > 3) {
        $task->ack();
    } else {
        $task->withHeader("attempts", $total_attempts)->withDelay(5)->requeue("failed");
    }
}
