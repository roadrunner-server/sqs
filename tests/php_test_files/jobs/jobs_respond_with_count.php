<?php

ini_set('display_errors', 'stderr');
require dirname(__DIR__) . "/vendor/autoload.php";

$consumer = new Spiral\RoadRunner\Jobs\Consumer();

while ($task = $consumer->waitTask()) {
    $count = $task->getHeaderLine('ApproximateReceiveCount');
    echo 'Receive count: ' . $count . PHP_EOL;
    $task->nack();
}
