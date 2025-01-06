package cwl

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

type LogGroup struct {
	client   *cloudwatchlogs.Client
	LogGroup types.LogGroup
}

func (lg *LogGroup) Name() string {
	return *lg.LogGroup.LogGroupName
}

func (lg *LogGroup) ARN() string {
	return *lg.LogGroup.LogGroupArn
}

func (lg *LogGroup) AccountID() string {
	arn := lg.ARN()
	parts := strings.Split(arn, ":")
	return parts[4]
}

func (lg *LogGroup) Region() string {
	arn := lg.ARN()
	parts := strings.Split(arn, ":")
	return parts[3]
}

func (lg *LogGroup) Stream(ctx context.Context) (*cloudwatchlogs.StartLiveTailEventStream, error) {
	output, err := lg.client.StartLiveTail(ctx, &cloudwatchlogs.StartLiveTailInput{
		LogGroupIdentifiers: []string{lg.ARN()},
	})
	if err != nil {
		return nil, err
	}

	return output.GetStream(), nil
}

func GetLogGroups(ctx context.Context, cfgs []aws.Config) ([]*LogGroup, error) {
	m := make(map[string]struct{})

	errs := []error{}

	logGroups := []*LogGroup{}
	wg := sync.WaitGroup{}
	for _, cfg := range cfgs {
		client := cloudwatchlogs.NewFromConfig(cfg)
		wg.Add(1)
		go func(client *cloudwatchlogs.Client) {
			defer wg.Done()
			var nextToken *string
			for {
				output, err := client.DescribeLogGroups(ctx, &cloudwatchlogs.DescribeLogGroupsInput{
					NextToken: nextToken,
					Limit:     aws.Int32(50),
				})
				if err != nil {
					errs = append(errs, err)
					return
				}
				for _, logGroup := range output.LogGroups {
					if _, ok := m[*logGroup.LogGroupArn]; ok {
						continue
					}
					m[*logGroup.LogGroupArn] = struct{}{}
					logGroups = append(logGroups, &LogGroup{
						client:   client,
						LogGroup: logGroup,
					})
				}
				nextToken = output.NextToken
				if nextToken == nil {
					break
				}
			}
		}(client)
	}
	wg.Wait()

	if len(logGroups) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, errors.New("no log groups found")
	}

	sort.Slice(logGroups, func(i, j int) bool {
		if logGroups[i].AccountID() != logGroups[j].AccountID() {
			return logGroups[i].AccountID() < logGroups[j].AccountID()
		}
		return *logGroups[i].LogGroup.LogGroupName < *logGroups[j].LogGroup.LogGroupName
	})

	return logGroups, nil
}

type LogEvent struct {
	msg       string
	timestamp time.Time
}

func NewLogEvent(evt types.LiveTailSessionLogEvent) *LogEvent {
	return &LogEvent{
		msg:       strings.ReplaceAll(strings.TrimSpace(*evt.Message), "\t", " "),
		timestamp: time.UnixMilli(*evt.Timestamp).In(time.FixedZone("Asia/Tokyo", 9*60*60)),
	}
}

func (e LogEvent) Timestamp() time.Time {
	return e.timestamp
}

func (e LogEvent) Message() string {
	return e.msg
}

func (e LogEvent) Lines(col int) []string {
	lines := []string{}
	line := ""
	for _, c := range e.msg {
		if c == '\n' {
			line += "\n"
			lines = append(lines, line)
			line = ""
		} else if unicode.IsPrint(c) {
			line += string(c)
			if len(line) >= col {
				lines = append(lines, line)
				line = ""
			}
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
