// ui/src/components/RecordDetail.tsx @ 3b71806061c90fe462ba857f788dc1ec7e2d2580; original lines 97-108
      {artifact && <Row label="path"><PreviewLink target={{ event: artifact.event, path: artifact.path, commit: artifact.commit }}><span className="font-mono">{artifact.path}</span></PreviewLink></Row>}
      {artifact && <Row label="at commit"><Id value={artifact.commit} /></Row>}
      {review && (
        <Row label="review">
          {review.verdict} by {nameOf(review.reviewer)}
          {review.head && <> over <Id value={review.head} /></>}
          {` · ${review.independence}`}
        </Row>
      )}
      {review?.artifact && <Row label="reviewed">{ref(review.artifact)}</Row>}
      {(decision || act) && (
        <Row label="fold">
